package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/j3yzz/kx/internal/forward"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
)

var forwardOpts struct {
	selector string
	address  string
}

var forwardCmd = &cobra.Command{
	Use:     "forward <app> [[local:]remote ...]",
	Aliases: []string{"pf"},
	Short:   "Forward local ports to an app, reconnecting when its pod restarts",
	Long: `Forwards local ports to the app's pod. With no port, the pod's only declared
port is used. A busy local port is replaced by a free one. If the pod restarts or
is deleted, the tunnel reconnects to the new pod (exponential backoff).
Binds to 127.0.0.1 unless --address is given.`,
	Example: `  kx forward mysql 3306
  kx forward mysql 13306:3306
  kx forward web 8080 9090`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		app := args[0]
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		rs := resolver.New(c)
		pick := func(ctx context.Context) (*corev1.Pod, error) {
			pods, err := rs.Resolve(ctx, c.Namespace, app, forwardOpts.selector)
			if err != nil {
				return nil, err
			}
			return resolver.PickPod(pods, false, nil, nil)
		}

		specs, err := forward.ParsePorts(args[1:])
		if err != nil {
			return err
		}
		if len(specs) == 0 {
			pod, err := pick(ctx)
			if err != nil {
				return err
			}
			if specs, err = forward.DefaultPorts(pod); err != nil {
				return err
			}
		}
		if specs, err = forward.AssignLocal(forwardOpts.address, specs, os.Stderr); err != nil {
			return err
		}

		return (&forward.Forwarder{
			Client:  c,
			App:     app,
			Ports:   specs,
			Address: forwardOpts.address,
			Resolve: pick,
			Out:     os.Stdout,
			Err:     os.Stderr,
		}).Run(ctx)
	},
}

func init() {
	f := forwardCmd.Flags()
	f.StringVarP(&forwardOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	f.StringVar(&forwardOpts.address, "address", "127.0.0.1", "local address to bind")
	rootCmd.AddCommand(forwardCmd)
}
