package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/codebase-foundation/cli/src/codebase/llm"
	"github.com/codebase-foundation/cli/src/codebase/tools"
	"github.com/codebase-foundation/cli/src/codebase/types"
)

// ──────────────────────────────────────────────────────────────
//  Agent
// ──────────────────────────────────────────────────────────────

const maxTurns = 30
const maxConsecutiveErrors = 5

const systemPrompt = `You are Codebase, a local AI coding agent running in the user's terminal.
You have direct access to their filesystem and shell. You help them build,
debug, and modify software projects.

Available tools:
- read_file: Read file contents with line numbers. Use offset/limit for large files.
- write_file: Create or overwrite a file. Parent directories are created automatically.
- edit_file: Surgical find-and-replace in a file. old_text must match exactly and be unique.
- multi_edit: Batch multiple edits across files. Per-file atomicity with rollback.
- list_files: List directory contents or glob for files (e.g. "**/*.go").
- search_files: Regex search across files (powered by ripgrep). Find definitions, usages, etc.
- web_search: Search the web. Use for current info, docs, versions, error solutions, or anything not in local files.
- dispatch_agent: Spawn a read-only research subagent to investigate questions in isolated context.
- shell: Run any shell command. Use for builds, tests, package management.
- git_status: Show working tree status (staged, unstaged, untracked files).
- git_diff: Show file diffs (staged, unstaged, or between refs).
- git_log: Show recent commit history.
- git_commit: Stage files and create a commit.
- git_branch: List, create, or switch branches.

Guidelines:
- You can call multiple tools in parallel — read_file, list_files, search_files, web_search, and dispatch_agent all run concurrently
- Use list_files and search_files to explore the project before making changes
- Read files before editing them — understand existing code first
- Make targeted, minimal changes — don't rewrite entire files unnecessarily
- For multiple related edits, prefer multi_edit over separate edit_file calls
- Use git tools instead of shell for git operations — they provide structured output
- After you edit files, the system may report diagnostics (errors, warnings) from language tools. If diagnostics appear, fix the issues before moving on.
- If a tool fails, read the error and try a different approach
- When finished, briefly summarize what you changed and why`

type Agent struct {
	Client    *llm.LLMClient
	WorkDir   string
	History   []types.ChatMessage
	Events    chan<- types.AgentEvent
	StopCh    <-chan struct{}
	Files     int // count of files created/modified
	PermCh    chan types.PermissionResponse
	PermState *types.PermissionState
	Diag      *tools.DiagnosticsEngine
}

func NewAgent(client *llm.LLMClient, workDir string, events chan<- types.AgentEvent, stopCh <-chan struct{}) *Agent {
	sysContent := buildSystemPrompt(workDir)
	return &Agent{
		Client:  client,
	WorkDir: workDir,
	Events:  events,
	StopCh:  stopCh,
	History: []types.ChatMessage{
			{Role: "system", Content: strPtr(sysContent)},
		},
		PermCh:    make(chan types.PermissionResponse, 1),
		PermState: &types.PermissionState{TrustedTools: map[string]bool{}},
		Diag:      tools.NewDiagnosticsEngine(workDir),
	}
}

func strPtr(s string) *string { return &s }

// buildSystemPrompt assembles the system prompt with project context.
func buildSystemPrompt(workDir string) string {
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString(fmt.Sprintf("\n\nCurrent date: %s\n", time.Now().Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))

	// Load project instructions if available
	projectInstructions := loadProjectInstructions(workDir)
	if projectInstructions != "" {
		sb.WriteString("\n## Project Instructions\n\n")
		sb.WriteString(projectInstructions)
		sb.WriteString("\n")
	}

	// Include top-level file tree
	tree := buildFileTree(workDir, 2)
	if tree != "" {
		sb.WriteString("\n## Project Structure\n\n```\n")
		sb.WriteString(tree)
		sb.WriteString("```\n")
	}

	return sb.String()
}

// loadProjectInstructions looks for project config files (AGENTS.md, CLAUDE.md,
// CODEX.md, .codebase) in the working directory and parent directories up to git root.
func loadProjectInstructions(workDir string) string {
	configFiles := []string{"AGENTS.md", "CLAUDE.md", "CODEX.md", ".codebase"}

	dir := workDir
	for {
		for _, name := range configFiles {
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err == nil && len(data) > 0 {
				content := string(data)
				// Cap at 20KB to avoid blowing up context
				if len(content) > 20*1024 {
					content = content[:20*1024] + "\n\n--- TRUNCATED (20KB limit) ---"
				}
				return content
			}
		}

		// Stop at git root or filesystem root
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return ""
}

// buildFileTree creates a simple tree listing of the project.
func buildFileTree(workDir string, maxDepth int) string {
	var sb strings.Builder
	buildTreeRecursive(&sb, workDir, workDir, "", maxDepth, 0)
	return sb.String()
}

func buildTreeRecursive(sb *strings.Builder, root, dir, prefix string, maxDepth, depth int) {
	if depth > maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Filter ignored directories
	var filtered []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && name != "." {
			continue
		}
		if e.IsDir() {
			if tools.IgnoreDirs[name] {
				continue
			}
		}
		filtered = append(filtered, e)
	}

	for i, e := range filtered {
		isLast := i == len(filtered)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		if e.IsDir() {
			fmt.Fprintf(sb, "%s%s%s/\n", prefix, connector, e.Name())
			buildTreeRecursive(sb, root, filepath.Join(dir, e.Name()), childPrefix, maxDepth, depth+1)
		} else {
			fmt.Fprintf(sb, "%s%s%s\n", prefix, connector, e.Name())
		}
	}
}

// Run executes the agent loop for a user prompt. Blocks until done.
func (a *Agent) Run(prompt string) {
	a.History = append(a.History, types.ChatMessage{
		Role:    "user",
		Content: strPtr(prompt),
	})

	consecutiveErrors := 0

	for turn := 1; turn <= maxTurns; turn++ {
		// Check for stop signal
		select {
		case <-a.StopCh:
			a.Events <- types.AgentEvent{Type: types.EventDone, Text: "Stopped by user."}
			return
		default:
		}

		// Check if compaction is needed before the LLM call
		if NeedsCompaction(a.History, a.Client.Model) {
			compacted, ok := CompactHistory(a.Client, a.History)
			if ok {
				a.History = compacted
			}
		}

		a.Events <- types.AgentEvent{Type: types.EventTurnStart, Turn: turn}

		// Stream LLM call
		streamCh := make(chan types.StreamEvent, 64)
		go a.Client.StreamChat(a.History, tools.ToolDefs, streamCh)

		var textContent strings.Builder
		var toolCalls []types.ToolCall
		var lastUsage types.ChunkUsage

		for evt := range streamCh {
			// Check stop between stream events
			select {
			case <-a.StopCh:
				a.Events <- types.AgentEvent{Type: types.EventDone, Text: "Stopped by user."}
				return
			default:
			}

			switch evt.Type {
			case types.StreamText:
				textContent.WriteString(evt.Text)
				a.Events <- types.AgentEvent{Type: types.EventTextDelta, Text: evt.Text}

			case types.StreamToolCalls:
				toolCalls = evt.ToolCalls

			case types.StreamUsage:
				lastUsage = evt.Usage
				a.Events <- types.AgentEvent{
					Type:   types.EventUsage,
					Tokens: types.TokenUsage{PromptTokens: evt.Usage.PromptTokens, CompletionTokens: evt.Usage.CompletionTokens},
				}

			case types.StreamError:
				a.Events <- types.AgentEvent{Type: types.EventError, Error: fmt.Errorf("%s", llm.HumanizeError(evt.Error))}
				a.Events <- types.AgentEvent{Type: types.EventDone, Text: "Error occurred."}
				return

			case types.StreamDone:
				// handled below
			}
		}

		_ = lastUsage

		// Build assistant message for history
		assistantMsg := types.ChatMessage{Role: "assistant"}
		txt := textContent.String()
		if txt != "" {
			assistantMsg.Content = strPtr(txt)
		}
		if len(toolCalls) > 0 {
			assistantMsg.ToolCalls = toolCalls
		}
		a.History = append(a.History, assistantMsg)

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			a.Events <- types.AgentEvent{Type: types.EventDone, Text: txt}
			return
		}

		// Execute tool calls — parallel for read-only, sequential for mutations
		a.executeToolCalls(toolCalls, &consecutiveErrors)

		if consecutiveErrors >= maxConsecutiveErrors {
			a.Events <- types.AgentEvent{
				Type: types.EventError,
				Error: fmt.Errorf("too many consecutive tool errors (%d), stopping", consecutiveErrors),
			}
			a.Events <- types.AgentEvent{Type: types.EventDone, Text: "Too many errors."}
			return
		}

		// Loop back for next turn
	}

	a.Events <- types.AgentEvent{Type: types.EventDone, Text: fmt.Sprintf("Reached maximum turns (%d). You can continue with a follow-up prompt.", maxTurns)}
}

// executeToolCalls runs tool calls with parallel execution for read-only tools.
func (a *Agent) executeToolCalls(toolCalls []types.ToolCall, consecutiveErrors *int) {
	// Classify tools
	var parallel []types.ToolCall
	var sequential []types.ToolCall
	for _, tc := range toolCalls {
		if tools.IsParallelSafe(tc.Function.Name) {
			parallel = append(parallel, tc)
		} else {
			sequential = append(sequential, tc)
		}
	}

	allErrors := true

	// Run read-only tools in parallel
	if len(parallel) > 0 {
		type result struct {
			tc      types.ToolCall
			args    map[string]any
			output  string
			success bool
		}
		results := make([]result, len(parallel))
		var wg sync.WaitGroup

		for i, tc := range parallel {
			var argsMap map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &argsMap); err != nil {
				argsMap = map[string]any{"_raw": tc.Function.Arguments}
			}

			a.Events <- types.AgentEvent{
				Type:   types.EventToolStart,
				Tool:   tc.Function.Name,
				ToolID: tc.ID,
				Args:   argsMap,
			}

			wg.Add(1)
			go func(idx int, tc types.ToolCall, args map[string]any) {
				defer wg.Done()
				var output string
				var success bool
				if tc.Function.Name == "dispatch_agent" {
					task := ""
					if args != nil {
						task, _ = args["task"].(string)
					}
					res, err := RunSubagent(a.Client, a.WorkDir, task)
					if err != nil {
						output = fmt.Sprintf("Subagent error: %v", err)
						success = false
					} else {
						output = res
						success = true
					}
				} else {
					output, success = tools.ExecuteTool(tc.Function.Name, tc.Function.Arguments, a.WorkDir)
				}
				results[idx] = result{tc: tc, args: args, output: output, success: success}
			}(i, tc, argsMap)
		}

		wg.Wait()

		for _, r := range results {
			if r.success {
				allErrors = false
			}

			a.Events <- types.AgentEvent{
				Type:    types.EventToolResult,
				Tool:    r.tc.Function.Name,
				ToolID:  r.tc.ID,
				Args:    r.args,
				Output:  r.output,
				Success: r.success,
			}

			a.History = append(a.History, types.ChatMessage{
				Role:       "tool",
				ToolCallID: r.tc.ID,
				Name:       r.tc.Function.Name,
				Content:    strPtr(r.output),
			})
		}
	}

	// Run mutating tools sequentially
	for _, tc := range sequential {
		var argsMap map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &argsMap); err != nil {
			argsMap = map[string]any{"_raw": tc.Function.Arguments}
		}

		// Check permission before executing
		if !a.checkPermission(tc.Function.Name, argsMap) {
			output := "Skipped: permission denied by user"
			a.Events <- types.AgentEvent{
				Type:   types.EventToolStart,
				Tool:   tc.Function.Name,
				ToolID: tc.ID,
				Args:   argsMap,
			}
			a.Events <- types.AgentEvent{
				Type:    types.EventToolResult,
				Tool:    tc.Function.Name,
				ToolID:  tc.ID,
				Args:    argsMap,
				Output:  output,
				Success: false,
			}
			a.History = append(a.History, types.ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    strPtr(output),
			})
			continue
		}

		a.Events <- types.AgentEvent{
			Type:   types.EventToolStart,
			Tool:   tc.Function.Name,
			ToolID: tc.ID,
			Args:   argsMap,
		}

		var output string
		var success bool
		if tc.Function.Name == "dispatch_agent" {
			task := ""
			if argsMap != nil {
				task, _ = argsMap["task"].(string)
			}
			res, err := RunSubagent(a.Client, a.WorkDir, task)
			if err != nil {
				output = fmt.Sprintf("Subagent error: %v", err)
				success = false
			} else {
				output = res
				success = true
			}
		} else {
			output, success = tools.ExecuteTool(tc.Function.Name, tc.Function.Arguments, a.WorkDir)
		}

		if success {
			allErrors = false
			if tc.Function.Name == "write_file" || tc.Function.Name == "edit_file" || tc.Function.Name == "multi_edit" {
				a.Files++
				a.maybeInjectDiagnostics(tc.Function.Name, argsMap)
			}
		}

		a.Events <- types.AgentEvent{
			Type:    types.EventToolResult,
			Tool:    tc.Function.Name,
			ToolID:  tc.ID,
			Args:    argsMap,
			Output:  output,
			Success: success,
		}

		a.History = append(a.History, types.ChatMessage{
			Role:       "tool",
			ToolCallID: tc.ID,
			Name:       tc.Function.Name,
			Content:    strPtr(output),
		})
	}

	if allErrors {
		*consecutiveErrors++
	} else {
		*consecutiveErrors = 0
	}
}

// FilesChanged returns how many files the agent has created/modified.
func (a *Agent) FilesChanged() int {
	return a.Files
}

// maybeInjectDiagnostics runs language checkers after file modifications
// and injects a system message with errors if found.
func (a *Agent) maybeInjectDiagnostics(toolName string, args map[string]any) {
	if a.Diag == nil || !a.Diag.Enabled {
		return
	}

	// Determine which files were modified
	var files []string
	switch toolName {
	case "write_file", "edit_file":
		if p, ok := args["path"].(string); ok {
			files = []string{p}
		}
	case "multi_edit":
		if edits, ok := args["edits"]; ok {
			if arr, ok := edits.([]interface{}); ok {
				seen := map[string]bool{}
				for _, e := range arr {
					if m, ok := e.(map[string]interface{}); ok {
						if p, ok := m["path"].(string); ok && !seen[p] {
							files = append(files, p)
							seen[p] = true
						}
					}
				}
			}
		}
	}

	if len(files) == 0 {
		return
	}

	diags := a.Diag.CheckFiles(files)
	if len(diags) == 0 {
		return
	}

	// Inject as system message so the LLM sees errors
	msg := tools.FormatDiagnosticsMessage(diags)
	a.History = append(a.History, types.ChatMessage{
		Role:    "system",
		Content: strPtr(msg),
	})
}

// checkPermission asks the TUI for permission if needed.
// Returns true if the tool should execute, false to skip.
func (a *Agent) checkPermission(toolName string, args map[string]any) bool {
	// Check session-level trust
	if a.PermState.Level == types.PermTrustAll {
		return true
	}
	if a.PermState.TrustedTools[toolName] {
		return true
	}

	// Check if this tool needs permission
	if !types.NeedsPermission(toolName, args) {
		return true
	}

	// Send permission request to TUI
	req := &types.PermissionRequest{
		Tool:    toolName,
		Args:    args,
		Summary: types.PermissionSummary(toolName, args),
	}
	a.Events <- types.AgentEvent{Type: types.EventPermission, Permission: req}

	// Block waiting for TUI response (or stop signal)
	select {
	case resp := <-a.PermCh:
		if resp.TrustLevel == types.PermTrustTool {
			a.PermState.TrustedTools[toolName] = true
		} else if resp.TrustLevel == types.PermTrustAll {
			a.PermState.Level = types.PermTrustAll
		}
		return resp.Allowed
	case <-a.StopCh:
		return false
	}
}
