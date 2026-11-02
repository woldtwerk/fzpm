// Package config stores user settings in $XDG_CONFIG_HOME/fzpm.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Config is the persisted user configuration.
type Config struct {
	// Disabled lists providers switched off in the settings pane. Providers
	// are enabled unless listed, so new ones show up automatically.
	Disabled []string `json:"disabled_providers,omitempty"`
	// SkipPrompt: outside any project, search all enabled providers right
	// away instead of asking which one to use.
	SkipPrompt bool `json:"skip_no_project_prompt,omitempty"`
	// TextIcons shows provider names instead of Nerd Font icons.
	TextIcons bool `json:"text_icons,omitempty"`
	// NoAutoUpdate stops fzpm from installing new releases at launch.
	NoAutoUpdate bool `json:"no_auto_update,omitempty"`

	path string
}

// Load reads the config; a missing file yields the defaults.
func Load() (*Config, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return &Config{}, err
	}
	return LoadFile(filepath.Join(dir, "fzpm", "config.json"))
}

// LoadFile reads the config from path.
func LoadFile(path string) (*Config, error) {
	c := &Config{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return c, err
	}
	c.path = path
	return c, nil
}

// Path is where the config is saved.
func (c *Config) Path() string { return c.path }

// Enabled reports whether a provider is enabled.
func (c *Config) Enabled(provider string) bool {
	return !slices.Contains(c.Disabled, provider)
}

// SetEnabled switches a provider on or off (in memory; call Save).
func (c *Config) SetEnabled(provider string, on bool) {
	c.Disabled = slices.DeleteFunc(c.Disabled, func(p string) bool { return p == provider })
	if !on {
		c.Disabled = append(c.Disabled, provider)
		slices.Sort(c.Disabled)
	}
}

// Save writes the config atomically.
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config path unknown")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
