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
	// Aliases give short names to apps: `kx exec pay` finds app "payments-web"
	// in its default namespace.
	Aliases map[string]Alias `json:"aliases"`
	// AuditLog set to false turns the audit log off (on by default).
	AuditLog *bool `json:"auditLog"`
	// Tunnels are saved `kx tunnel <name>` profiles.
	Tunnels map[string]Tunnel `json:"tunnels"`
}

// Tunnel is a saved port-forward: app, ports ("3306" or "13306:3306"), an
// optional namespace, and an optional local client command. In Client, {host}
// and {port} (the first local port) are replaced; kx stops the tunnel when
// the client exits.
type Tunnel struct {
	App       string   `json:"app"`
	Namespace string   `json:"namespace"`
	Ports     []string `json:"ports"`
	Client    string   `json:"client"`
}

// Alias maps a short name to an app and, optionally, a default namespace
// (an explicit -n still wins).
type Alias struct {
	App       string `json:"app"`
	Namespace string `json:"namespace"`
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
	for name, a := range c.Aliases {
		if a.App == "" {
			return nil, fmt.Errorf("%s: alias %q needs an app", path, name)
		}
	}
	for name, t := range c.Tunnels {
		if t.App == "" || len(t.Ports) == 0 {
			return nil, fmt.Errorf("%s: tunnel %q needs an app and at least one port", path, name)
		}
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

// AuditEnabled reports whether sensitive actions are written to the audit log.
func (c *Config) AuditEnabled() bool { return c.AuditLog == nil || *c.AuditLog }
