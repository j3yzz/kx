package cmd

import (
	"fmt"
	"os"

	"github.com/j3yzz/kx/internal/configmaps"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
)

var cmOpts struct {
	key      string
	selector string
}

var configmapsCmd = &cobra.Command{
	Use:     "configmaps <app>",
	Aliases: []string{"cm"},
	Short:   "Show the ConfigMaps an app uses and where each key is consumed",
	Long: `Like 'kx secrets', for ConfigMaps (env configMapKeyRef, envFrom, configMap
volumes). Values are shown: ConfigMaps are configuration, not credentials.`,
	Example: `  kx configmaps payments
  kx cm payments --key LOG_LEVEL -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		if err := requireAccess(c, listPods, getConfigMap); err != nil {
			return err
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, app, cmOpts.selector)
		if err != nil {
			return err
		}
		var refs []configmaps.Ref
		for _, p := range resolver.Representatives(pods) {
			refs = append(refs, configmaps.Walk(&p)...)
		}
		entries, err := configmaps.Collect(cmd.Context(), c, c.Namespace, refs)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Fprintf(os.Stderr, "app %s uses no ConfigMaps\n", app)
		}
		return configmaps.Render(os.Stdout, configmaps.FilterKey(entries, cmOpts.key), Global.Output)
	},
}

func init() {
	f := configmapsCmd.Flags()
	f.StringVar(&cmOpts.key, "key", "", "only this key")
	f.StringVarP(&cmOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	configmapsCmd.ValidArgsFunction = complete(listApps)
	rootCmd.AddCommand(configmapsCmd)
}
