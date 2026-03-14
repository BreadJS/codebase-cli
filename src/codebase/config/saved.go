package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SavedConfig holds the persisted user settings.
type SavedConfig struct {
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	Model   string `json:"model,omitempty"`
}

// ConfigPath returns the path to the user's config file.
func ConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codebase", "config.json")
}

// LoadSavedConfig loads the user's saved config from disk.
func LoadSavedConfig() SavedConfig {
	path := ConfigPath()
	if path == "" {
		return SavedConfig{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SavedConfig{}
	}
	var sc SavedConfig
	json.Unmarshal(data, &sc)
	return sc
}

// SaveSavedConfig saves the user's config to disk.
func SaveSavedConfig(sc SavedConfig) error {
	path := ConfigPath()
	if path == "" {
		return fmt.Errorf("cannot determine home directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
