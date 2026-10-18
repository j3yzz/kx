// Package guard sits between the commands and the cluster. Every action that
// reaches into a pod or exposes a Secret passes through Check, so a new
// feature gets the prod protection by calling it, not by reimplementing it.
package guard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

type Action string

const (
	Exec    Action = "open a shell / run a command in"
	Forward Action = "port-forward to"
	Reveal  Action = "reveal Secret values of"
)

// ErrDeclined means the user answered no at the confirmation prompt.
var ErrDeclined = errors.New("cancelled")

type Guard struct {
	ProdContexts []string
	Yes          bool // --yes: confirm in advance
	Interactive  bool // stdin is a terminal, so we can ask
	In           io.Reader
	Out          io.Writer
}

// IsProd reports whether the context matches a prod entry (exact or glob).
func (g *Guard) IsProd(context string) bool {
	for _, p := range g.ProdContexts {
		if ok, _ := path.Match(p, context); ok {
			return true
		}
	}
	return false
}

// Check returns nil to proceed. In a non-prod context it always does. In a
// prod one it needs --yes or an interactive "y".
func (g *Guard) Check(context, namespace string, a Action, target string) error {
	if !g.IsProd(context) || g.Yes {
		return nil
	}
	if !g.Interactive {
		return fmt.Errorf("context %q is marked prod; pass --yes to %s %s without a prompt", context, a, target)
	}
	fmt.Fprintf(g.Out, "Context %q is marked prod. %s %s in namespace %s. Continue? [y/N]: ", context, capitalize(string(a)), target, namespace)
	line, _ := bufio.NewReader(g.In).ReadString('\n')
	if ans := strings.ToLower(strings.TrimSpace(line)); ans == "y" || ans == "yes" {
		return nil
	}
	return ErrDeclined
}

func capitalize(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
