package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/j3yzz/kx/internal/logs"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
)

var logsOpts struct {
	container string
	selector  string
	follow    bool
	previous  bool
	since     time.Duration
	tail      int64
}

var logsCmd = &cobra.Command{
	Use:   "logs <app>",
	Short: "Logs from all pods of an app, prefixed per pod",
	Long: `Prints the logs of every pod of the app, each line prefixed with the pod name
(colored per pod on a terminal). Without -f the pods are printed one after another;
with -f they stream together and pods created during a rollout are picked up.
The container is -c, else the default-container annotation, else the first one.`,
	Example: `  kx logs payments
  kx logs payments -f --since 10m
  kx logs payments --previous --tail 200`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		rs := resolver.New(c)
		src := func(ctx context.Context) ([]corev1.Pod, error) {
			return rs.Resolve(ctx, c.Namespace, app, logsOpts.selector)
		}
		return logs.Run(ctx, c, src, logs.Options{
			Container: logsOpts.container,
			Follow:    logsOpts.follow,
			Previous:  logsOpts.previous,
			Since:     logsOpts.since,
			Tail:      logsOpts.tail,
			Color:     useColor(os.Stdout),
		}, os.Stdout, os.Stderr)
	},
}

func init() {
	f := logsCmd.Flags()
	f.BoolVarP(&logsOpts.follow, "follow", "f", false, "stream logs, including pods created later")
	f.BoolVar(&logsOpts.previous, "previous", false, "logs of the previous (crashed) container instance")
	f.DurationVar(&logsOpts.since, "since", 0, "only logs newer than this, e.g. 10m")
	f.Int64Var(&logsOpts.tail, "tail", -1, "lines per pod (-1 = all)")
	f.StringVarP(&logsOpts.container, "container", "c", "", "container name")
	f.StringVarP(&logsOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	logsCmd.ValidArgsFunction = complete(listApps)
	rootCmd.AddCommand(logsCmd)
}
