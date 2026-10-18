// Package exec runs a command in a pod container over the Kubernetes exec API
// (WebSocket, falling back to SPDY), with full TTY support.
package exec

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/j3yzz/kx/internal/kube"
	"golang.org/x/term"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ShellCommand is used when the user gives no command: bash if the container
// has it, else sh. One round trip, and unlike matching on exit code 127 it
// cannot misfire when the user's last bash command was "not found".
var ShellCommand = []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"}

// Target is the pod container to run in.
type Target struct {
	Namespace, Pod, Container string
}

// Run executes command (ShellCommand if empty) in t, wired to the process's
// stdin/stdout/stderr. With a terminal on stdin and stdout it allocates a TTY,
// puts the local terminal in raw mode (so Ctrl+C reaches the pod) and forwards
// resizes. A non-zero remote exit surfaces as a k8s.io/client-go/util/exec.ExitError.
func Run(ctx context.Context, c *kube.Client, t Target, command []string) error {
	if len(command) == 0 {
		command = ShellCommand
	}
	tty := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))

	req := c.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(t.Namespace).Name(t.Pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: t.Container,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    !tty, // a TTY merges stderr into stdout
			TTY:       tty,
		}, scheme.ParameterCodec)

	spdy, err := remotecommand.NewSPDYExecutor(c.Config, "POST", req.URL())
	if err != nil {
		return err
	}
	ws, err := remotecommand.NewWebSocketExecutor(c.Config, "GET", req.URL().String())
	if err != nil {
		return err
	}
	ex, err := remotecommand.NewFallbackExecutor(ws, spdy, httpstream.IsUpgradeFailure)
	if err != nil {
		return err
	}

	opts := remotecommand.StreamOptions{Stdin: os.Stdin, Stdout: os.Stdout, Tty: tty}
	if !tty {
		opts.Stderr = os.Stderr
		return ex.StreamWithContext(ctx, opts)
	}

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), old)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	opts.TerminalSizeQueue = newSizeQueue(ctx, os.Stdout)
	return ex.StreamWithContext(ctx, opts)
}

// sizeQueue emits the current terminal size, then a new one on every SIGWINCH.
type sizeQueue struct {
	ch chan remotecommand.TerminalSize
}

func newSizeQueue(ctx context.Context, tty *os.File) *sizeQueue {
	q := &sizeQueue{ch: make(chan remotecommand.TerminalSize, 1)}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	send := func() {
		if w, h, err := term.GetSize(int(tty.Fd())); err == nil {
			select {
			case <-q.ch: // drop a stale size nobody consumed
			default:
			}
			q.ch <- remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}
		}
	}
	send()
	go func() {
		defer signal.Stop(winch)
		defer close(q.ch)
		for {
			select {
			case <-winch:
				send()
			case <-ctx.Done():
				return
			}
		}
	}()
	return q
}

func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	s, ok := <-q.ch
	if !ok {
		return nil
	}
	return &s
}
