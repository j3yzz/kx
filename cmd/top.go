package cmd

import (
	"os"

	"github.com/j3yzz/kx/internal/resolver"
	"github.com/j3yzz/kx/internal/top"
	"github.com/spf13/cobra"
)

var topOpts struct{ selector string }

var topCmd = &cobra.Command{
	Use:   "top <app>",
	Short: "CPU and memory per pod next to requests and limits",
	Long:  "Needs metrics-server in the cluster. Percentages are of the limit.",
	Args:  orPick(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		args, err := pickAppArg(cmd, args)
		if err != nil {
			return err
		}
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		if err := requireAccess(c, listPods); err != nil {
			return err
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, app, topOpts.selector)
		if err != nil {
			return err
		}
		ms, err := top.Fetch(cmd.Context(), c, c.Namespace)
		if err != nil {
			return err
		}
		rows, err := top.Build(pods, ms)
		if err != nil {
			return err
		}
		return top.Render(os.Stdout, rows, Global.Output)
	},
}

func init() {
	topCmd.Flags().StringVarP(&topOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	topCmd.ValidArgsFunction = complete(listApps)
	rootCmd.AddCommand(topCmd)
}
