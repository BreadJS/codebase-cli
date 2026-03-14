package theme

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// wrapText wraps a string to the given width on word boundaries.
// Copied here to avoid circular imports between theme and ui packages.
func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	var result strings.Builder
	for _, paragraph := range strings.Split(s, "\n") {
		if paragraph == "" {
			result.WriteString("\n")
			continue
		}
		// Preserve leading whitespace
		trimmed := strings.TrimLeft(paragraph, " \t")
		indent := paragraph[:len(paragraph)-len(trimmed)]
		indentW := lipgloss.Width(indent)
		effectiveWidth := width - indentW
		if effectiveWidth < 10 {
			effectiveWidth = 10
		}

		words := strings.Fields(trimmed)
		lineLen := 0
		for i, word := range words {
			wl := lipgloss.Width(word)
			if lineLen+wl+1 > effectiveWidth && lineLen > 0 {
				result.WriteString("\n")
				result.WriteString(indent)
				lineLen = 0
			}
			if lineLen > 0 {
				result.WriteString(" ")
				lineLen++
			} else if i == 0 {
				result.WriteString(indent)
			}
			result.WriteString(word)
			lineLen += wl
		}
		result.WriteString("\n")
	}
	return strings.TrimRight(result.String(), "\n")
}

// highlightCode applies syntax highlighting to a code string.
// lang is the language hint from the fence tag (e.g. "go", "python").
// Returns ANSI-colored string, or the original code on failure.
func highlightCode(code, lang string) string {
	// Find lexer
	var lexer chroma.Lexer
	if lang != "" {
		lexer = lexers.Get(lang)
	}
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		return code
	}
	lexer = chroma.Coalesce(lexer)

	// Pick style based on theme
	styleName := chromaStyleName()
	style := styles.Get(styleName)
	if style == nil {
		style = styles.Fallback
	}

	// Tokenize
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}

	// Format to terminal 256-color
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return code
	}

	return strings.TrimRight(buf.String(), "\n")
}

// chromaStyleName returns the chroma style based on the active theme.
func chromaStyleName() string {
	if ActiveTheme.Name == "light" {
		return "github"
	}
	return "monokai"
}

// renderMarkdownText processes text with code fence detection.
// Code blocks get syntax highlighting and a left border accent.
// Prose sections get word-wrapped.
func renderMarkdownText(text string, width int) string {
	if width <= 0 {
		return text
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")
	inCodeBlock := false
	var codeLang string
	var codeLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect code fence start/end
		if strings.HasPrefix(trimmed, "```") {
			if !inCodeBlock {
				// Opening fence
				inCodeBlock = true
				codeLang = strings.TrimPrefix(trimmed, "```")
				codeLang = strings.TrimSpace(codeLang)
				codeLines = nil
			} else {
				// Closing fence — render the collected code
				code := strings.Join(codeLines, "\n")
				highlighted := highlightCode(code, codeLang)
				borderStyle := fgStyle(ActiveTheme.Dim)

				for _, hl := range strings.Split(highlighted, "\n") {
					result.WriteString(borderStyle.Render("  │ ") + hl + "\n")
				}

				inCodeBlock = false
				codeLang = ""
				codeLines = nil
			}
			continue
		}

		if inCodeBlock {
			codeLines = append(codeLines, line)
		} else {
			// Markdown prose formatting
			styled := renderMarkdownLine(trimmed, width)
			result.WriteString(styled)
			result.WriteString("\n")
		}
	}

	// Handle unclosed code block (streaming may have flushed mid-block)
	if inCodeBlock && len(codeLines) > 0 {
		code := strings.Join(codeLines, "\n")
		highlighted := highlightCode(code, codeLang)
		borderStyle := fgStyle(ActiveTheme.Dim)

		for _, hl := range strings.Split(highlighted, "\n") {
			result.WriteString(borderStyle.Render("  │ ") + hl + "\n")
		}
	}

	return strings.TrimRight(result.String(), "\n")
}

// ──────────────────────────────────────────────────────────────
//  Inline markdown rendering
// ──────────────────────────────────────────────────────────────

var (
	reInlineCode = regexp.MustCompile("`([^`]+)`")
	reBold       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic     = regexp.MustCompile(`\*(.+?)\*`)
)

// renderMarkdownLine applies block-level and inline formatting to a single line.
func renderMarkdownLine(line string, width int) string {
	if line == "" {
		return ""
	}

	// Headers: # H1, ## H2, ### H3
	if strings.HasPrefix(line, "### ") {
		text := strings.TrimPrefix(line, "### ")
		return lipgloss.NewStyle().Foreground(colAccent).Render(text)
	}
	if strings.HasPrefix(line, "## ") {
		text := strings.TrimPrefix(line, "## ")
		return lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render(text)
	}
	if strings.HasPrefix(line, "# ") {
		text := strings.TrimPrefix(line, "# ")
		return lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render(text)
	}

	// Horizontal rules
	if line == "---" || line == "***" || line == "___" {
		if width > 4 {
			return styleDim.Render(strings.Repeat("─", width-2))
		}
		return styleDim.Render("───")
	}

	// Bullet lists: - item, * item, + item
	if len(line) > 2 && (line[0] == '-' || line[0] == '*' || line[0] == '+') && line[1] == ' ' {
		bullet := styleDim.Render("  •")
		text := applyInlineFormatting(line[2:])
		return bullet + " " + wrapText(text, width-4)
	}

	// Numbered lists: 1. item, 2. item
	if len(line) > 2 && line[0] >= '1' && line[0] <= '9' {
		dotIdx := strings.Index(line, ". ")
		if dotIdx > 0 && dotIdx <= 3 {
			num := styleDim.Render("  " + line[:dotIdx+1])
			text := applyInlineFormatting(line[dotIdx+2:])
			return num + " " + wrapText(text, width-5)
		}
	}

	// Regular text with inline formatting
	formatted := applyInlineFormatting(line)
	return wrapText(formatted, width)
}

// applyInlineFormatting applies bold, italic, and inline code styling.
func applyInlineFormatting(text string) string {
	// Inline code: `code` → styled
	text = reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		code := m[1 : len(m)-1]
		return lipgloss.NewStyle().Foreground(colCyan).Render(code)
	})

	// Bold: **text** → styled
	text = reBold.ReplaceAllStringFunc(text, func(m string) string {
		inner := m[2 : len(m)-2]
		return lipgloss.NewStyle().Bold(true).Render(inner)
	})

	// Italic: *text* → styled (after bold so ** is processed first)
	text = reItalic.ReplaceAllStringFunc(text, func(m string) string {
		inner := m[1 : len(m)-1]
		return lipgloss.NewStyle().Italic(true).Render(inner)
	})

	return text
}
