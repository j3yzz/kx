// Package audit appends one JSON line per sensitive action to a local file, so
// "who opened a shell in prod, and when" has an answer. Only the action and its
// target are recorded, never command arguments or Secret values.
package audit

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

type Entry struct {
	Time      time.Time `json:"time"`
	User      string    `json:"user"`
	Context   string    `json:"context"`
	Namespace string    `json:"namespace"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Outcome   string    `json:"outcome"` // "allowed" or "declined"
}

// Path is $XDG_STATE_HOME/kx/audit.log, else ~/.local/state/kx/audit.log.
func Path() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "kx", "audit.log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "kx", "audit.log"), nil
}

// Append writes e to path (private to the user), creating it as needed.
// O_APPEND keeps concurrent kx processes from tearing each other's lines.
func Append(path string, e Entry) error {
	if e.User == "" {
		if u, err := user.Current(); err == nil {
			e.User = u.Username
		}
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(e)
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
