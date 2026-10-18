package cmd

import (
	"os"
	"time"

	"github.com/j3yzz/kx/internal/resolver"
	"github.com/j3yzz/kx/internal/status"
	"github.com/spf13/cobra"
)

var statusOpts struct {
	selector string
	events   int
}

var statusCmd = &cobra.Command{
	Use:   "status <app>",
	Short: "Health summary: replicas, restarts, last termination reason, recent events",
	Example: `  kx status payments
  kx status payments -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, app, statusOpts.selector)
		if err != nil {
			return err
		}
		r := status.Collect(cmd.Context(), c, c.Namespace, pods, time.Now(), statusOpts.events)
		return status.Render(os.Stdout, r, Global.Output)
	},
}

func init() {
	f := statusCmd.Flags()
	f.StringVarP(&statusOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	f.IntVar(&statusOpts.events, "events", 10, "number of recent events to show")
	statusCmd.ValidArgsFunction = complete(listApps)
	rootCmd.AddCommand(statusCmd)
}
