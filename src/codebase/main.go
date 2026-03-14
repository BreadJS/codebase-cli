package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/codebase-foundation/cli/src/codebase/config"
	"github.com/codebase-foundation/cli/src/codebase/ui"
)

// Set by goreleaser ldflags
var (
	version = "dev"
	commit  = "none"
)

// ──────────────────────────────────────────────────────────────
//  Configuration
// ──────────────────────────────────────────────────────────────

func loadConfig() (*config.Config, error) {
	// CLI flags
	model := flag.String("model", "", "LLM model name (default: gpt-4o)")
	dir := flag.String("dir", "", "Working directory (default: current dir)")
	baseURL := flag.String("base-url", "", "OpenAI-compatible API base URL")
	resume := flag.Bool("resume", false, "Resume previous session for this directory")
	noBoot := flag.Bool("no-boot", false, "Skip boot animation")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("codebase %s (%s)\n", version, commit)
		os.Exit(0)
	}

	cfg := &config.Config{}

	// API key: env var → saved config → interactive prompt
	cfg.APIKey = os.Getenv("OPENAI_API_KEY")
	if cfg.APIKey == "" {
		saved := config.LoadSavedConfig()
		cfg.APIKey = saved.APIKey
	}
	if cfg.APIKey == "" {
		key, err := promptForAPIKey()
		if err != nil {
			return nil, err
		}
		cfg.APIKey = key
	}

	// Base URL
	cfg.BaseURL = os.Getenv("OPENAI_BASE_URL")
	if *baseURL != "" {
		cfg.BaseURL = *baseURL
	}
	if cfg.BaseURL == "" {
		saved := config.LoadSavedConfig()
		if saved.BaseURL != "" {
			cfg.BaseURL = saved.BaseURL
		} else {
			cfg.BaseURL = "https://api.openai.com/v1"
		}
	}

	// Model
	cfg.Model = os.Getenv("OPENAI_MODEL")
	if *model != "" {
		cfg.Model = *model
	}
	if cfg.Model == "" {
		saved := config.LoadSavedConfig()
		if saved.Model != "" {
			cfg.Model = saved.Model
		} else {
			cfg.Model = "gpt-4o"
		}
	}

	// Working directory
	if *dir != "" {
		abs, err := filepath.Abs(*dir)
		if err != nil {
			return nil, fmt.Errorf("invalid directory: %w", err)
		}
		cfg.WorkDir = abs
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
		cfg.WorkDir = wd
	}

	// Verify work dir exists
	info, err := os.Stat(cfg.WorkDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("working directory does not exist: %s", cfg.WorkDir)
	}

	// Resume flag
	cfg.Resume = *resume

	// No boot: flag or env var
	cfg.NoBoot = *noBoot || os.Getenv("CODEBASE_NOBOOT") != ""

	return cfg, nil
}

func promptForAPIKey() (string, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("  Welcome to Codebase!")
	fmt.Println()
	fmt.Println("  No API key found. You need an OpenAI-compatible API key to use Codebase.")
	fmt.Println("  Get one from: https://platform.openai.com/api-keys")
	fmt.Println()
	fmt.Print("  Enter your API key: ")

	key, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("no API key provided")
	}

	// Ask about base URL for non-OpenAI providers
	fmt.Println()
	fmt.Println("  Using OpenAI by default. If you use a different provider (Groq, local, etc.),")
	fmt.Print("  enter the base URL (or press Enter to skip): ")

	baseInput, _ := reader.ReadString('\n')
	baseURL := strings.TrimSpace(baseInput)

	// Save for next time
	sc := config.LoadSavedConfig()
	sc.APIKey = key
	if baseURL != "" {
		sc.BaseURL = baseURL
	}
	if err := config.SaveSavedConfig(sc); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not save config: %v\n", err)
	} else {
		fmt.Println()
		fmt.Println("  Saved to ~/.codebase/config.json")
	}
	fmt.Println()

	return key, nil
}

// ──────────────────────────────────────────────────────────────
//  Entry point
// ──────────────────────────────────────────────────────────────

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Clean up stale sessions in the background
	go config.CleanStaleSessions()

	p := tea.NewProgram(
		ui.NewAppModel(cfg),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
