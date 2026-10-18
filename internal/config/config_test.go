package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil || len(c.ProdContexts) != 0 || c.ClipboardClear() != 30*time.Second {
		t.Fatalf("missing file should give defaults: %+v %v", c, err)
	}

	c, err = Load(write(t, "prodContexts: [prod, '*-prod']\nclipboardClearSeconds: 5\n"))
	if err != nil || len(c.ProdContexts) != 2 || c.ClipboardClear() != 5*time.Second {
		t.Fatalf("%+v %v", c, err)
	}

	c, _ = Load(write(t, "clipboardClearSeconds: 0\n"))
	if c.ClipboardClear() != 0 {
		t.Fatal("0 must mean never clear, not default")
	}
}

func TestLoadRejects(t *testing.T) {
	for name, body := range map[string]string{
		"typo (would silently disable the guard)": "prodContext: [prod]\n",
		"negative delay": "clipboardClearSeconds: -1\n",
		"bad yaml":       "prodContexts: [",
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
