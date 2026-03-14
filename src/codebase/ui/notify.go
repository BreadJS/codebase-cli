package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/codebase-foundation/cli/src/codebase/llm"
	"github.com/codebase-foundation/cli/src/codebase/theme"
)

// ──────────────────────────────────────────────────────────────
//  Toast notification rendering for the TUI
//
//  llm.Notifications appear as small animated bars above the main
//  viewport content. Each has a type-specific color, icon, and
//  lifecycle (fade in → visible → fade out).
//
//  The Glue LLM controls what text appears; this layer controls
//  how it looks and animates.
// ──────────────────────────────────────────────────────────────

// notifyManager holds active notifications and renders them.
type notifyManager struct {
	active []llm.Notification
	frame  int // animation frame counter
}

func newNotifyManager() *notifyManager {
	return &notifyManager{}
}

// Push adds a notification to the display stack.
// Replaces existing progress notifications (only one at a time).
func (nm *notifyManager) Push(n llm.Notification) {
	n.CreatedAt = time.Now()

	// Progress notifications replace each other
	if n.Type == llm.NotifyProgress {
		for i, existing := range nm.active {
			if existing.Type == llm.NotifyProgress {
				nm.active[i] = n
				return
			}
		}
	}

	nm.active = append(nm.active, n)

	// Cap at 4 visible notifications
	if len(nm.active) > 4 {
		nm.active = nm.active[len(nm.active)-4:]
	}
}

// Tick advances the animation frame and prunes expired notifications.
func (nm *notifyManager) Tick() {
	nm.frame++

	// Remove expired
	alive := nm.active[:0]
	for _, n := range nm.active {
		if !n.IsExpired() {
			alive = append(alive, n)
		}
	}
	nm.active = alive
}

// ClearProgress removes all progress-type notifications.
func (nm *notifyManager) ClearProgress() {
	alive := nm.active[:0]
	for _, n := range nm.active {
		if n.Type != llm.NotifyProgress {
			alive = append(alive, n)
		}
	}
	nm.active = alive
}

// HasActive returns true if there are visible notifications.
func (nm *notifyManager) HasActive() bool {
	return len(nm.active) > 0
}

// Render produces the notification bar string(s) for the TUI.
// Returns empty string if no active notifications.
func (nm *notifyManager) Render(width int) string {
	if len(nm.active) == 0 {
		return ""
	}

	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}

	var sb strings.Builder
	for _, n := range nm.active {
		line := nm.renderOne(n, innerW)
		if line != "" {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// renderOne renders a single notification with type-specific styling.
func (nm *notifyManager) renderOne(n llm.Notification, width int) string {
	age := time.Since(n.CreatedAt)
	dur := n.DefaultDuration()

	// Fade lifecycle: 0-200ms fade in, then solid, last 500ms fade out
	var opacity float64
	switch {
	case age < 200*time.Millisecond:
		opacity = float64(age) / float64(200*time.Millisecond)
	case age > dur-500*time.Millisecond:
		remaining := dur - age
		opacity = float64(remaining) / float64(500*time.Millisecond)
	default:
		opacity = 1.0
	}
	opacity = math.Max(0.1, math.Min(1.0, opacity))

	icon := nm.icon(n.Type)
	text := n.Text
	if lipgloss.Width(text) > width-10 {
		runes := []rune(text)
		if len(runes) > width-13 {
			text = string(runes[:width-13]) + "..."
		}
	}

	// Style based on type — fade from dim to type color
	dimHex := string(colDim)
	var style lipgloss.Style
	switch n.Type {
	case llm.NotifyInfo:
		fg := theme.LerpColor(dimHex, string(colAccent), opacity)
		style = lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	case llm.NotifyProgress:
		fg := theme.LerpColor(dimHex, string(colCyan), opacity)
		style = lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	case llm.NotifySuccess:
		fg := theme.LerpColor(dimHex, string(colSuccess), opacity)
		style = lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	case llm.NotifyWarn:
		fg := theme.LerpColor(dimHex, string(colWarning), opacity)
		style = lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	case llm.NotifyCelebrate:
		fg := theme.LerpColor(dimHex, string(colPurple), opacity)
		style = lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Bold(true)
	}

	content := fmt.Sprintf("  %s %s", icon, text)

	// Celebrate gets cascading sparkle animation
	if n.Type == llm.NotifyCelebrate {
		s1 := nm.sparkleFrameAt(nm.frame)
		s2 := nm.sparkleFrameAt(nm.frame + 3)
		s3 := nm.sparkleFrameAt(nm.frame + 6)
		content = fmt.Sprintf("  %s %s %s %s %s", s1, s2, style.Render(text), s3, s1)
		return content
	}

	return style.Render(content)
}

// icon returns the leading icon for a notification type.
func (nm *notifyManager) icon(t llm.NotifyType) string {
	switch t {
	case llm.NotifyInfo:
		return styleMuted.Render("›")
	case llm.NotifyProgress:
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		progColor := spinnerColors[(nm.frame/3)%len(spinnerColors)]
		return lipgloss.NewStyle().Foreground(progColor).Render(frames[nm.frame%len(frames)])
	case llm.NotifySuccess:
		return styleOK.Render("✓")
	case llm.NotifyWarn:
		return styleWarn.Render("!")
	case llm.NotifyCelebrate:
		return lipgloss.NewStyle().Foreground(colPurple).Render("★")
	default:
		return "›"
	}
}

// sparkleFrame returns an animated sparkle character at the current frame.
func (nm *notifyManager) sparkleFrame() string {
	return nm.sparkleFrameAt(nm.frame)
}

// sparkleFrameAt returns a sparkle character at a specific frame offset for cascading effects.
func (nm *notifyManager) sparkleFrameAt(frame int) string {
	sparkles := []string{"✦", "✧", "⋆", "★", "·", "✧", "✦", "⋆"}
	colors := []lipgloss.Color{colPurple, colCyan, colAccent, colSuccess, colOrange, colPurple, colCyan, colAccent}
	idx := frame % len(sparkles)
	return lipgloss.NewStyle().Foreground(colors[idx]).Bold(true).Render(sparkles[idx])
}

// ──────────────────────────────────────────────────────────────
//  Suggestion rendering
// ──────────────────────────────────────────────────────────────

// renderSuggestions formats follow-up suggestions as a compact bar.
func renderSuggestions(suggestions []string, width int) string {
	if len(suggestions) == 0 {
		return ""
	}

	sep := styleDim.Render("  ·  ")

	// Build progressively, dropping suggestions that don't fit
	var parts []string
	for i, s := range suggestions {
		num := lipgloss.NewStyle().Foreground(colDim).Render(fmt.Sprintf("[%d]", i+1))
		text := lipgloss.NewStyle().Foreground(colSecondary).Render(s)
		candidate := num + " " + text
		trial := "  " + strings.Join(append(parts, candidate), sep)
		if lipgloss.Width(trial) > width-4 && len(parts) > 0 {
			break // don't add this one — previous suggestions fit
		}
		parts = append(parts, candidate)
	}

	return "  " + strings.Join(parts, sep)
}
