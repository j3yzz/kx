package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type clip struct{ copyCmd, clearCmd []string }

// First tool found wins.
var clips = []clip{
	{[]string{"pbcopy"}, []string{"pbcopy"}},
	{[]string{"wl-copy"}, []string{"wl-copy", "--clear"}},
	{[]string{"xclip", "-selection", "clipboard"}, []string{"xclip", "-selection", "clipboard"}},
	{[]string{"xsel", "-ib"}, []string{"xsel", "-ib"}},
}

// Copy puts val on the clipboard. If clearAfter > 0 a detached shell blanks the
// clipboard after the delay. The value only travels over the tool's stdin:
// never argv, env, disk or logs.
// ponytail: clears unconditionally, even if the user copied something else in
// the meantime; compare-before-clear if that bites.
func Copy(val []byte, clearAfter time.Duration) error {
	for _, c := range clips {
		if _, err := exec.LookPath(c.copyCmd[0]); err != nil {
			continue
		}
		cmd := exec.Command(c.copyCmd[0], c.copyCmd[1:]...)
		cmd.Stdin = bytes.NewReader(val)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s failed: %v", c.copyCmd[0], err) // err from exec, never contains the value
		}
		if clearAfter > 0 {
			script := fmt.Sprintf("sleep %d; %s </dev/null", int(clearAfter.Seconds()), strings.Join(c.clearCmd, " "))
			bg := exec.Command("sh", "-c", script)
			bg.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := bg.Start(); err != nil {
				return fmt.Errorf("copied, but could not schedule clipboard clear: %v", err)
			}
			_ = bg.Process.Release()
		}
		return nil
	}
	return errors.New("no clipboard tool found (need pbcopy, wl-copy, xclip or xsel)")
}
