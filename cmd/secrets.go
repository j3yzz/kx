package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/j3yzz/kx/internal/secrets"
	"github.com/spf13/cobra"
)

var secretsOpts struct {
	reveal     bool
	copy       bool
	key        string
	selector   string
	clearAfter time.Duration
}

var secretsCmd = &cobra.Command{
	Use:     "secrets <app>",
	Aliases: []string{"sec"},
	Short:   "Show the Secrets an app uses and where each key is consumed",
	Long: `Reads the app's pod to find the Secrets it uses (env secretKeyRef, envFrom,
secret volumes, imagePullSecrets). Values are masked unless --reveal is given.
Read-only: kx never writes Secrets.`,
	Example: `  kx secrets payments
  kx secrets payments --reveal --key DB_PASSWORD
  kx secrets payments --key DB_PASSWORD --copy
  kx secrets payments -o env --reveal > .env`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		if err := guardReveal(c, app); err != nil {
			return err
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, app, secretsOpts.selector)
		if err != nil {
			return err
		}
		// Pods of one component share a spec; walk the newest of each.
		var refs []secrets.Ref
		for _, p := range resolver.Representatives(pods) {
			refs = append(refs, secrets.Walk(&p)...)
		}
		entries, err := secrets.Collect(cmd.Context(), c, c.Namespace, refs)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Fprintf(os.Stderr, "app %s uses no Secrets\n", app)
		}
		return outputSecrets(cmd, entries)
	},
}

var secretsGetCmd = &cobra.Command{
	Use:   "get <secret-name>",
	Short: "Show one Secret by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		if err := guardReveal(c, args[0]); err != nil {
			return err
		}
		entries, err := secrets.Collect(cmd.Context(), c, c.Namespace,
			[]secrets.Ref{{Secret: args[0], Consumer: "-"}})
		if err != nil {
			return err
		}
		return outputSecrets(cmd, entries)
	},
}

// guardReveal confirms in prod contexts, but only when values will be exposed.
func guardReveal(c *kube.Client, target string) error {
	if !secretsOpts.reveal && !secretsOpts.copy {
		return nil
	}
	return guardCheck(c, guard.Reveal, target)
}

// clearAfter is --copy-clear if given, else the config value.
func clearAfter(cmd *cobra.Command) (time.Duration, error) {
	if cmd.Flags().Changed("copy-clear") {
		return secretsOpts.clearAfter, nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return 0, err
	}
	return cfg.ClipboardClear(), nil
}

func outputSecrets(cmd *cobra.Command, entries []secrets.Entry) error {
	entries = secrets.FilterKey(entries, secretsOpts.key)
	if secretsOpts.copy {
		e, err := secrets.Single(entries)
		if err != nil {
			return err
		}
		clear, err := clearAfter(cmd)
		if err != nil {
			return err
		}
		if err := secrets.Copy(e.Value, clear); err != nil {
			return err
		}
		msg := fmt.Sprintf("copied %s/%s to clipboard", e.Secret, e.Key)
		if clear > 0 {
			msg += fmt.Sprintf(" (clears in %s)", clear)
		}
		fmt.Fprintln(os.Stderr, msg)
		return nil
	}
	return secrets.Render(os.Stdout, entries, Global.Output, secretsOpts.reveal)
}

func init() {
	f := secretsCmd.PersistentFlags()
	f.BoolVar(&secretsOpts.reveal, "reveal", false, "show decoded values instead of masking them")
	f.StringVar(&secretsOpts.key, "key", "", "only this key")
	f.BoolVar(&secretsOpts.copy, "copy", false, "copy one key's value to the clipboard (needs --key unless one key matches)")
	f.DurationVar(&secretsOpts.clearAfter, "copy-clear", 30*time.Second, "clear the clipboard after this delay, 0 to keep (default: config clipboardClearSeconds, else 30s)")
	secretsCmd.Flags().StringVarP(&secretsOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	secretsCmd.AddCommand(secretsGetCmd)
	rootCmd.AddCommand(secretsCmd)
}
