package cmd

import (
	"os"

	"github.com/j3yzz/kx/internal/env"
	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
)

var envOpts struct {
	container string
	selector  string
	reveal    bool
}

var envCmd = &cobra.Command{
	Use:   "env <app>",
	Short: "Effective environment of an app's pod, with the source of each variable",
	Long: `Shows every environment variable a container gets and where it comes from:
a literal, a ConfigMap key, a Secret key, or a field/resource reference. env entries
override envFrom, as Kubernetes does. Secret values are masked unless --reveal.`,
	Example: `  kx env payments
  kx env payments -c sidecar -o json
  kx env payments --reveal -o env > .env`,
	Args: orPick(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		args, err := pickAppArg(cmd, args)
		if err != nil {
			return err
		}
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		if err := requireAccess(c, listPods, getConfigMap, getSecrets); err != nil {
			return err
		}
		if envOpts.reveal {
			if err := guardCheck(c, guard.Reveal, app); err != nil {
				return err
			}
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, app, envOpts.selector)
		if err != nil {
			return err
		}
		// Pods of one component share a spec; use the newest of each.
		reps := resolver.Representatives(pods)
		var all []env.Var
		for i := range reps {
			vars, err := env.Collect(cmd.Context(), c, &reps[i])
			if err != nil {
				return err
			}
			for _, v := range vars {
				if envOpts.container != "" && v.Container != envOpts.container {
					continue
				}
				if len(reps) > 1 { // keep components with equal container names apart
					v.Container = reps[i].Labels["app.kubernetes.io/name"] + "/" + v.Container
				}
				all = append(all, v)
			}
		}
		return env.Render(os.Stdout, all, Global.Output, envOpts.reveal)
	},
}

func init() {
	f := envCmd.Flags()
	f.BoolVar(&envOpts.reveal, "reveal", false, "show Secret-sourced values instead of masking them")
	f.StringVarP(&envOpts.container, "container", "c", "", "only this container")
	f.StringVarP(&envOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	envCmd.ValidArgsFunction = complete(listApps)
	rootCmd.AddCommand(envCmd)
}
