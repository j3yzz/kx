package exec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Runs ShellCommand locally against a PATH holding only fake shells.
func TestShellCommandFallback(t *testing.T) {
	for _, withBash := range []bool{true, false} {
		dir := t.TempDir()
		fake := func(name string) {
			body := "#!/bin/sh\necho " + name + "\n"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		fake("sh")
		if withBash {
			fake("bash")
		}
		cmd := exec.Command("/bin/sh", "-c", ShellCommand[2])
		cmd.Env = []string{"PATH=" + dir}
		out, err := cmd.Output()
		want := "sh"
		if withBash {
			want = "bash"
		}
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Errorf("withBash=%v: got %q err %v, want %q", withBash, out, err, want)
		}
	}
}
