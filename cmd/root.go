package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
)

// Exit codes. Documented in `kx --help`.
const (
	ExitOK           = 0
	ExitError        = 1
	ExitAppNotFound  = 2
	ExitAccessDenied = 3
)

// CodeError carries a specific process exit code. A zero-value Err prints nothing.
type CodeError struct {
	Code int
	Err  error
}

func (e *CodeError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *CodeError) Unwrap() error { return e.Err }

// Global holds flags shared by every command.
var Global struct {
	Namespace  string
	Context    string
	Kubeconfig string
	Output     string
	NoColor    bool
	Yes        bool
}

var rootCmd = &cobra.Command{
	Use:   "kx",
	Short: "Faster Kubernetes CLI: exec, forward and secrets by application name",
	Long: `kx finds pods by application name (Deployment/StatefulSet name or the
app.kubernetes.io/name and app labels), so you never copy a pod name.

Shell completion (app names, namespaces, contexts):
  source <(kx completion zsh)      # or bash; fish: kx completion fish | source

Exit codes:
  0  success
  1  general error
  2  application not found
  3  access denied`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(*cobra.Command, []string) error {
		if o := Global.Output; o != "table" && o != "json" && o != "env" {
			return fmt.Errorf("invalid -o %q: want table, json or env (env: secrets only)", o)
		}
		return nil
	},
}

func init() {
	f := rootCmd.PersistentFlags()
	f.StringVarP(&Global.Namespace, "namespace", "n", "", "namespace (default: from kubeconfig)")
	f.StringVar(&Global.Context, "context", "", "kubeconfig context")
	f.StringVar(&Global.Kubeconfig, "kubeconfig", "", "path to kubeconfig")
	f.StringVarP(&Global.Output, "output", "o", "table", "output format: table|json (secrets also: env)")
	f.BoolVarP(&Global.Yes, "yes", "y", false, "confirm prod-context prompts in advance")
	f.BoolVar(&Global.NoColor, "no-color", false, "disable colored output")
	registerFlagCompletions()
}

// Execute runs the root command and exits with the matching code.
func Execute() {
	err := rootCmd.ExecuteContext(context.Background())
	if err == nil {
		os.Exit(ExitOK)
	}
	code := ExitError
	var ce *CodeError
	if errors.As(err, &ce) {
		code = ce.Code
	} else if errors.Is(err, kube.ErrDenied) {
		code = ExitAccessDenied
	} else if errors.Is(err, resolver.ErrNotFound) {
		code = ExitAppNotFound
	}
	if msg := err.Error(); msg != "" {
		fmt.Fprintln(os.Stderr, "kx:", msg)
	}
	os.Exit(code)
}

// newClient builds the kube client from the global flags and announces
// context/namespace on stderr when they differ from the defaults.
func newClient() (*kube.Client, error) { return newClientIn("") }

// newAppClient is newClient for commands that take an app name: it expands a
// configured alias and uses the alias's namespace unless -n was given. It
// returns the real app name to resolve.
func newAppClient(name string) (*kube.Client, string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, "", err
	}
	a, ok := cfg.Aliases[name]
	if !ok {
		c, err := newClientIn("")
		return c, name, err
	}
	c, err := newClientIn(a.Namespace)
	return c, a.App, err
}

// newClientIn builds the client; defaultNS applies only when -n is not given.
func newClientIn(defaultNS string) (*kube.Client, error) {
	// Fail on a bad config before doing anything else: a typo in prodContexts
	// must not quietly disable the prod guard.
	if _, err := loadConfig(); err != nil {
		return nil, err
	}
	ns := Global.Namespace
	if ns == "" {
		ns = defaultNS
	}
	c, err := kube.New(kube.Options{
		Kubeconfig: Global.Kubeconfig,
		Context:    Global.Context,
		Namespace:  ns,
	})
	if err != nil {
		return nil, err
	}
	prod := (&guard.Guard{ProdContexts: cfgCache.ProdContexts}).IsProd(c.Context)
	c.Announce(os.Stderr, prod, prod && useColor(os.Stderr))
	return c, nil
}

// useColor reports whether to color output on f: only on a terminal, and not
// with --no-color or NO_COLOR set.
func useColor(f *os.File) bool {
	return !Global.NoColor && os.Getenv("NO_COLOR") == "" && kube.IsTTY(f)
}
