package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/j3yzz/kx/internal/secrets"
	"github.com/spf13/cobra"
)

var diffOpts struct {
	toNamespace, toContext, toApp string
	byKey                         bool
}

var secretsDiffCmd = &cobra.Command{
	Use:   "diff <app>",
	Short: "Compare an app's Secrets between two namespaces or contexts (never shows values)",
	Long: `Compares which Secret keys the app uses on the current side (-n/--context) and on
the other side (--to-namespace / --to-context), and whether each value is the same.
Values are never printed: only per-run fingerprints, equal when the values are equal.`,
	Example: `  kx secrets diff payments -n payments-stg --to-namespace payments-prod
  kx secrets diff payments --context stg --to-context prod
  kx secrets diff pay-stage-web -n stg --to-namespace prd --to-app pay-prd-web`,
	Args: orPick(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		args, err := pickAppArg(cmd, args)
		if err != nil {
			return err
		}
		if secretsOpts.reveal || secretsOpts.copy {
			return errors.New("diff never shows values; --reveal and --copy do not apply")
		}
		if diffOpts.toNamespace == "" && diffOpts.toContext == "" && diffOpts.toApp == "" {
			return errors.New("give the other side: --to-namespace, --to-context and/or --to-app")
		}
		lc, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		rc, err := kube.New(kube.Options{
			Kubeconfig: Global.Kubeconfig,
			Context:    firstNonEmpty(diffOpts.toContext, Global.Context),
			Namespace:  firstNonEmpty(diffOpts.toNamespace, lc.Namespace),
		})
		if err != nil {
			return err
		}
		if lc.Context == rc.Context && lc.Namespace == rc.Namespace && (diffOpts.toApp == "" || diffOpts.toApp == app) {
			return errors.New("both sides are the same context, namespace and app")
		}

		var entries [2][]secrets.Entry
		for i, c := range []*kube.Client{lc, rc} {
			if err := requireAccess(c, listPods, getSecrets); err != nil {
				return err
			}
			name := app
			if i == 1 && diffOpts.toApp != "" {
				name = diffOpts.toApp
			}
			pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, name, secretsOpts.selector)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", c.Context, c.Namespace, err)
			}
			var refs []secrets.Ref
			for _, p := range resolver.Representatives(pods) {
				refs = append(refs, secrets.Walk(&p)...)
			}
			if entries[i], err = secrets.Collect(cmd.Context(), c, c.Namespace, refs); err != nil {
				return err
			}
		}

		if diffOpts.byKey { // Secret names differ per environment: compare keys across all Secrets
			for _, side := range entries {
				for i := range side {
					side[i].Secret = ""
				}
			}
		}
		fp, err := secrets.NewFingerprinter()
		if err != nil {
			return err
		}
		rows := secrets.FilterDiffKey(secrets.Diff(entries[0], entries[1], fp), secretsOpts.key)
		return secrets.RenderDiff(os.Stdout, rows, Global.Output,
			lc.Context+"/"+lc.Namespace, rc.Context+"/"+rc.Namespace)
	},
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func init() {
	f := secretsDiffCmd.Flags()
	f.StringVar(&diffOpts.toNamespace, "to-namespace", "", "namespace of the other side")
	f.BoolVar(&diffOpts.byKey, "by-key", false, "ignore Secret names: compare keys across all of the app's Secrets (names often differ per environment)")
	f.StringVar(&diffOpts.toApp, "to-app", "", "app name on the other side, when it differs (e.g. -stage- vs -prd-)")
	f.StringVar(&diffOpts.toContext, "to-context", "", "kubeconfig context of the other side")
	f.StringVarP(&secretsOpts.selector, "selector", "l", "", "label selector to find the app's pods (both sides)")
	secretsDiffCmd.ValidArgsFunction = complete(listApps)
	secretsCmd.AddCommand(secretsDiffCmd)
}
