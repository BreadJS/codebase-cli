package types

// ──────────────────────────────────────────────────────────────
//  Config type
// ──────────────────────────────────────────────────────────────

// Config holds the application configuration.
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	WorkDir string
	Resume  bool   // --resume flag: restore previous session
	NoBoot  bool   // --no-boot flag: skip boot animation
}

// ──────────────────────────────────────────────────────────────
//  Shared types used across packages
// ──────────────────────────────────────────────────────────────

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents a function call from the LLM.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents the function being called.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// TokenUsage tracks token counts for API requests.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ──────────────────────────────────────────────────────────────
//  Agent event types
// ──────────────────────────────────────────────────────────────

// EventType represents the type of agent event.
type EventType int

const (
	EventTextDelta  EventType = iota // streaming text chunk
	EventToolStart                   // tool execution starting
	EventToolResult                  // tool execution done
	EventUsage                       // token count update
	EventTurnStart                   // new agentic turn
	EventDone                        // agent finished all turns
	EventError                       // error occurred
	EventPermission                  // permission request for TUI
)

// PermissionRequest represents a permission request from the agent.
type PermissionRequest struct {
	Tool    string      `json:"tool"`
	Args    map[string]any `json:"args"`
	Summary string      `json:"summary"`
}

// PermissionResponse represents a permission response from the TUI.
type PermissionResponse struct {
	Allowed    bool           `json:"allowed"`
	TrustLevel PermissionLevel `json:"trust_level"`
}

// PermissionState tracks permission state for the agent session.
type PermissionState struct {
	Level        PermissionLevel   `json:"level"`
	TrustedTools map[string]bool   `json:"trusted_tools"`
}

// AgentEvent represents events from the agent.
type AgentEvent struct {
	Type       EventType             `json:"type"`
	Text       string                `json:"text"`
	Tool       string                `json:"tool,omitempty"`
	ToolID     string                `json:"tool_id,omitempty"`
	Args       map[string]any        `json:"args,omitempty"`
	Output     string                `json:"output,omitempty"`
	Success    bool                  `json:"success,omitempty"`
	Tokens     TokenUsage            `json:"tokens,omitempty"`
	Turn       int                   `json:"turn,omitempty"`
	Error      error                 `json:"error,omitempty"`
	Permission *PermissionRequest    `json:"permission,omitempty"`
}

// ──────────────────────────────────────────────────────────────
//  LLM types
// ──────────────────────────────────────────────────────────────

// StreamEventType represents types of stream events.
type StreamEventType int

const (
	StreamText      StreamEventType = iota // text content delta
	StreamToolCalls                        // complete, accumulated tool calls
	StreamUsage                            // token usage
	StreamDone                             // stream finished
	StreamError                            // error
)

// StreamEvent represents a parsed SSE stream event.
type StreamEvent struct {
	Type      StreamEventType
	Text      string
	ToolCalls []ToolCall
	Usage     ChunkUsage
	Error     error
}

// ChunkUsage represents token usage from a streaming chunk.
type ChunkUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ToolDef represents a tool definition for the LLM.
type ToolDef struct {
	Type     string         `json:"type"`
	Function ToolDefFunction `json:"function"`
}

// ToolDefFunction represents the function definition.
type ToolDefFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// ──────────────────────────────────────────────────────────────
//  Streaming types (for internal use in llm package)
// ──────────────────────────────────────────────────────────────

// ToolCallDelta represents a delta in a tool call during streaming.
type ToolCallDelta struct {
	Index    *int                `json:"index,omitempty"`
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function *FunctionCallDelta  `json:"function,omitempty"`
}

// FunctionCallDelta represents a delta in a function call during streaming.
type FunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}
