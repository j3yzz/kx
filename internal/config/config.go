// Package config reads ~/.config/kx/config.yaml. A missing file means defaults.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

const defaultClipboardClear = 30 * time.Second

type Config struct {
	// ProdContexts are kubeconfig context names (globs allowed, e.g. "*-prod")
	// where exec, forward and reveal ask for confirmation.
	ProdContexts []string `json:"prodContexts"`
	// ClipboardClearSeconds is the delay before --copy clears the clipboard.
	// Unset means 30; 0 means never clear.
	ClipboardClearSeconds *int `json:"clipboardClearSeconds"`
}

// Path returns the default config file location.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "kx", "config.yaml"), nil
}

// Load reads path. Unknown fields are an error: a typo such as "prodContext"
// would otherwise silently switch the prod guard off.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.UnmarshalStrict(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s := c.ClipboardClearSeconds; s != nil && *s < 0 {
		return nil, fmt.Errorf("%s: clipboardClearSeconds must be >= 0", path)
	}
	return &c, nil
}

// ClipboardClear is how long --copy keeps a value on the clipboard (0 = forever).
func (c *Config) ClipboardClear() time.Duration {
	if c.ClipboardClearSeconds == nil {
		return defaultClipboardClear
	}
	return time.Duration(*c.ClipboardClearSeconds) * time.Second
}
