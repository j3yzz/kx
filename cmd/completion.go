package cmd

import (
	"context"
	"strings"
	"time"

	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Shell completion. `kx completion bash|zsh|fish` is Cobra's built-in command;
// this file supplies the dynamic values. Completion must be quiet and fast, so
// it builds the client without the context announcement, caps each lookup at
// 3s, and on any error offers nothing.

type lister func(ctx context.Context, c *kube.Client) ([]string, error)

func complete(list lister) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 { // only the first positional is a name
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		c, err := kube.New(kube.Options{Kubeconfig: Global.Kubeconfig, Context: Global.Context, Namespace: Global.Namespace})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		names, err := list(ctx, c)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return prefixed(names, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func prefixed(names []string, p string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, p) {
			out = append(out, n)
		}
	}
	return out
}

func listApps(ctx context.Context, c *kube.Client) ([]string, error) {
	return resolver.New(c).Apps(ctx, c.Namespace)
}

func listSecretNames(ctx context.Context, c *kube.Client) ([]string, error) {
	l, err := c.CoreV1().Secrets(c.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, len(l.Items))
	for i, s := range l.Items {
		names[i] = s.Name
	}
	return names, nil
}

func init() {
	apps := complete(listApps)
	execCmd.ValidArgsFunction = apps
	forwardCmd.ValidArgsFunction = apps
	secretsCmd.ValidArgsFunction = apps
	secretsGetCmd.ValidArgsFunction = complete(listSecretNames)
}

// registerFlagCompletions is called from root.go's init, after the flags
// exist (init order across files is alphabetical, so completion.go runs first).
func registerFlagCompletions() {
	must(rootCmd.RegisterFlagCompletionFunc("namespace", func(_ *cobra.Command, _ []string, p string) ([]string, cobra.ShellCompDirective) {
		names, _ := complete(func(ctx context.Context, c *kube.Client) ([]string, error) {
			l, err := c.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err != nil {
				return nil, err
			}
			out := make([]string, len(l.Items))
			for i, n := range l.Items {
				out[i] = n.Name
			}
			return out, nil
		})(nil, nil, p)
		return names, cobra.ShellCompDirectiveNoFileComp
	}))
	must(rootCmd.RegisterFlagCompletionFunc("context", func(_ *cobra.Command, _ []string, p string) ([]string, cobra.ShellCompDirective) {
		names, err := kube.Contexts(Global.Kubeconfig)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return prefixed(names, p), cobra.ShellCompDirectiveNoFileComp
	}))
}

// must turns a wiring mistake into a startup panic instead of silently lost completion.
func must(err error) {
	if err != nil {
		panic(err)
	}
}
