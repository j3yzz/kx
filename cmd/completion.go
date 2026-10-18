package cmd

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/j3yzz/kx/internal/config"
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
	names, err := resolver.New(c).Apps(ctx, c.Namespace)
	if cfg, cerr := loadConfig(); cerr == nil {
		names = mergeAliases(names, cfg.Aliases)
	}
	return names, err
}

// mergeAliases adds alias names to names: sorted, no duplicates.
func mergeAliases(names []string, aliases map[string]config.Alias) []string {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	for a := range aliases {
		set[a] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// completeContainers offers the container names of the app's pods for -c.
func completeContainers(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	const no = cobra.ShellCompDirectiveNoFileComp
	if len(args) == 0 {
		return nil, no
	}
	cfg, err := loadConfig()
	if err != nil {
		return nil, no
	}
	app, ns := args[0], Global.Namespace
	if a, ok := cfg.Aliases[app]; ok {
		app = a.App
		if ns == "" {
			ns = a.Namespace
		}
	}
	c, err := kube.New(kube.Options{Kubeconfig: Global.Kubeconfig, Context: Global.Context, Namespace: ns})
	if err != nil {
		return nil, no
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pods, err := resolver.New(c).Resolve(ctx, c.Namespace, app, "")
	if err != nil {
		return nil, no
	}
	set := map[string]bool{}
	for _, p := range pods {
		for _, ct := range p.Spec.Containers {
			set[ct.Name] = true
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return prefixed(names, toComplete), no
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
	for _, c := range []*cobra.Command{execCmd, logsCmd, envCmd} {
		must(c.RegisterFlagCompletionFunc("container", completeContainers))
	}
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
