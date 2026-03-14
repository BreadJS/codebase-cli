package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/codebase-foundation/cli/src/codebase/types"
)

// ──────────────────────────────────────────────────────────────
//  OpenAI Chat Completions types (for API marshaling)
// ──────────────────────────────────────────────────────────────

// Note: types.ChatMessage, ToolCall, FunctionCall, types.ToolDef are now in types package
// and shared across packages. These aliases exist for convenience.

// ──────────────────────────────────────────────────────────────
//  Streaming chunk types
// ──────────────────────────────────────────────────────────────

type StreamChunk struct {
	Choices []StreamChoice `json:"choices"`
	Usage   *types.ChunkUsage    `json:"usage,omitempty"`
}

type StreamChoice struct {
	Delta        StreamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type StreamDelta struct {
	Content   *string              `json:"content,omitempty"`
	ToolCalls []types.ToolCallDelta `json:"tool_calls,omitempty"`
}


// ──────────────────────────────────────────────────────────────
//  LLM Client
// ──────────────────────────────────────────────────────────────

type LLMClient struct {
	APIKey  string
	BaseURL string
	Model   string
	client  *http.Client
}

func NewLLMClient(apiKey, baseURL, model string) *LLMClient {
	return newLLMClientWithTimeout(apiKey, baseURL, model, 5*time.Minute)
}

func newLLMClientWithTimeout(apiKey, baseURL, model string, timeout time.Duration) *LLMClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o"
	}
	return &LLMClient{
		APIKey:  apiKey,
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		Model:   model,
		client:  &http.Client{Timeout: timeout},
	}
}

// StreamChat sends a streaming Chat Completions request and pushes
// parsed events into the provided channel. Caller should read from ch
// until it is closed.
func (c *LLMClient) StreamChat(messages []types.ChatMessage, tools []types.ToolDef, ch chan<- types.StreamEvent) {
	defer close(ch)

	body := map[string]interface{}{
		"model":    c.Model,
		"messages": messages,
		"stream":   true,
		"stream_options": map[string]interface{}{
			"include_usage": true,
		},
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
		body["parallel_tool_calls"] = true
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		ch <- types.StreamEvent{Type: types.StreamError, Error: fmt.Errorf("marshal: %w", err)}
		return
	}

	// Retry transient errors (429, 502, 503) up to 3 times with backoff
	var resp *http.Response
	maxRetries := 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("POST", c.BaseURL+"/chat/completions", bytes.NewReader(jsonBody))
		if err != nil {
			ch <- types.StreamEvent{Type: types.StreamError, Error: fmt.Errorf("request: %w", err)}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Accept", "text/event-stream")

		resp, err = c.client.Do(req)
		if err != nil {
			ch <- types.StreamEvent{Type: types.StreamError, Error: fmt.Errorf("connection error: %v", err)}
			return
		}

		// Retry on transient HTTP errors
		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 {
			resp.Body.Close()
			if attempt < maxRetries {
				backoff := time.Duration(1<<uint(attempt)) * time.Second
				time.Sleep(backoff)
				continue
			}
		}
		break
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		ch <- types.StreamEvent{Type: types.StreamError, Error: fmt.Errorf("API error %d: %s", resp.StatusCode, truncateErrorBody(string(errBody)))}
		return
	}

	// Parse SSE stream
	var accumulated []types.ToolCall
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // skip malformed chunks
		}

		// Usage (usually in the final chunk)
		if chunk.Usage != nil {
			ch <- types.StreamEvent{Type: types.StreamUsage, Usage: *chunk.Usage}
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		delta := choice.Delta

		// Text content
		if delta.Content != nil && *delta.Content != "" {
			ch <- types.StreamEvent{Type: types.StreamText, Text: *delta.Content}
		}

		// Tool call deltas — accumulate progressively
		for i, tcd := range delta.ToolCalls {
			idx := i
			if tcd.Index != nil {
				idx = *tcd.Index
			}
			// Grow the slice
			for len(accumulated) <= idx {
				accumulated = append(accumulated, types.ToolCall{Type: "function"})
			}
			if tcd.ID != "" {
				accumulated[idx].ID = tcd.ID
			}
			if tcd.Function != nil {
				if tcd.Function.Name != "" {
					accumulated[idx].Function.Name = tcd.Function.Name
				}
				accumulated[idx].Function.Arguments += tcd.Function.Arguments
			}
		}

		// Check for finish
		if choice.FinishReason != nil {
			if len(accumulated) > 0 {
				// Emit tool calls on any finish reason — some providers
				// use "stop" instead of "tool_calls"
				ch <- types.StreamEvent{Type: types.StreamToolCalls, ToolCalls: accumulated}
				accumulated = nil
			}
		}
	}

	ch <- types.StreamEvent{Type: types.StreamDone}
}

// truncateErrorBody shortens raw API error bodies for display.
func truncateErrorBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// HumanizeError converts raw Go/API errors into user-friendly messages.
func HumanizeError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "Cannot connect to the API server. Check your OPENAI_BASE_URL."
	case strings.Contains(msg, "no such host"):
		return "DNS resolution failed. Check your network connection and OPENAI_BASE_URL."
	case strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "Client.Timeout"):
		return "Request timed out. The API server may be overloaded."
	case strings.Contains(msg, "API error 401"):
		return "Authentication failed. Check your OPENAI_API_KEY."
	case strings.Contains(msg, "API error 403"):
		return "Access denied. Your API key may not have permission for this model."
	case strings.Contains(msg, "API error 404"):
		return "Model not found. Check your model name."
	case strings.Contains(msg, "API error 429"):
		return "Rate limited. Too many requests — please wait a moment."
	default:
		// Cap length for display
		if len(msg) > 200 {
			return msg[:200] + "..."
		}
		return msg
	}
}

// NonStreamingChat makes a non-streaming Chat Completions call.
func NonStreamingChat(client *LLMClient, messages []types.ChatMessage) (string, error) {
	body := map[string]interface{}{
		"model":    client.Model,
		"messages": messages,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", client.BaseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.APIKey)

	resp, err := client.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API %d: %s", resp.StatusCode, string(errBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}
