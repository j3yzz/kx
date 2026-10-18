package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/j3yzz/kx/internal/kube"
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
}

var rootCmd = &cobra.Command{
	Use:   "kx",
	Short: "Faster Kubernetes CLI: exec, forward and secrets by application name",
	Long: `kx finds pods by application name (Deployment/StatefulSet name or the
app.kubernetes.io/name and app labels), so you never copy a pod name.

Exit codes:
  0  success
  1  general error
  2  application not found
  3  access denied`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(*cobra.Command, []string) error {
		if Global.Output != "table" && Global.Output != "json" {
			return fmt.Errorf("invalid -o %q: want table or json", Global.Output)
		}
		return nil
	},
}

func init() {
	f := rootCmd.PersistentFlags()
	f.StringVarP(&Global.Namespace, "namespace", "n", "", "namespace (default: from kubeconfig)")
	f.StringVar(&Global.Context, "context", "", "kubeconfig context")
	f.StringVar(&Global.Kubeconfig, "kubeconfig", "", "path to kubeconfig")
	f.StringVarP(&Global.Output, "output", "o", "table", "output format: table|json")
	f.BoolVar(&Global.NoColor, "no-color", false, "disable colored output")
}

// Execute runs the root command and exits with the matching code.
func Execute() {
	err := rootCmd.Execute()
	if err == nil {
		os.Exit(ExitOK)
	}
	code := ExitError
	var ce *CodeError
	if errors.As(err, &ce) {
		code = ce.Code
	} else if errors.Is(err, kube.ErrDenied) {
		code = ExitAccessDenied
	}
	if msg := err.Error(); msg != "" {
		fmt.Fprintln(os.Stderr, "kx:", msg)
	}
	os.Exit(code)
}

// newClient builds the kube client from the global flags and announces
// context/namespace on stderr when they differ from the defaults.
func newClient() (*kube.Client, error) {
	c, err := kube.New(kube.Options{
		Kubeconfig: Global.Kubeconfig,
		Context:    Global.Context,
		Namespace:  Global.Namespace,
	})
	if err != nil {
		return nil, err
	}
	c.Announce(os.Stderr)
	return c, nil
}
