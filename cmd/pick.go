package cmd

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/picker"
	"github.com/spf13/cobra"
)

// orPick lets an app command run with no app argument when stdin is a
// terminal: pickAppArg then asks which app. Otherwise next decides.
func orPick(next cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && cmd.ArgsLenAtDash() == -1 && kube.IsTTY(os.Stdin) {
			return nil
		}
		if len(args) == 0 && cmd.ArgsLenAtDash() == -1 {
			return errors.New("app name required (on a terminal, omit it to pick one interactively)")
		}
		return next(cmd, args)
	}
}

// pickAppArg returns args with a chosen app prepended when none was given.
func pickAppArg(cmd *cobra.Command, args []string) ([]string, error) {
	if len(args) > 0 || cmd.ArgsLenAtDash() >= 0 {
		return args, nil
	}
	if !kube.IsTTY(os.Stdin) || !kube.IsTTY(os.Stderr) {
		return nil, errors.New("app name required")
	}
	c, err := kube.New(kube.Options{Kubeconfig: Global.Kubeconfig, Context: Global.Context, Namespace: Global.Namespace})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	names, err := listApps(ctx, c)
	if err != nil && len(names) == 0 {
		return nil, err
	}
	app, err := picker.Pick(os.Stdin, os.Stderr, "app in "+c.Namespace, names)
	if err != nil {
		return nil, err
	}
	return append([]string{app}, args...), nil
}
