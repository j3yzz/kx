package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/j3yzz/kx/internal/debug"
	kxexec "github.com/j3yzz/kx/internal/exec"
	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
)

var debugOpts struct {
	image    string
	target   string
	selector string
}

var debugCmd = &cobra.Command{
	Use:   "debug <app> [-c target] [--image img] [-- cmd...]",
	Short: "Open a shell in a debug container next to an app's container (for images with no shell)",
	Long: `Adds an ephemeral container to a pod of the app and runs a shell in it. It shares
the process namespace of the target container (-c, else the default container), so
you can inspect its processes even when its image has no shell. The ephemeral
container cannot be removed: it stays until the pod is replaced.`,
	Example: `  kx debug payments
  kx debug payments --image nicolaka/netshoot
  kx debug payments -c app -- ps aux`,
	Args: orPick(appThenCommand),
	RunE: func(cmd *cobra.Command, args []string) error {
		args, err := pickAppArg(cmd, args)
		if err != nil {
			return err
		}
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		if err := requireAccess(c, getPods, listPods, ephemeral, execPods); err != nil {
			return err
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, app, debugOpts.selector)
		if err != nil {
			return err
		}
		pod, err := resolver.PickPod(pods, false, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
		target, err := resolver.PickContainer(pod, debugOpts.target, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
		if err := guardCheck(c, guard.Exec, app+" ("+pod.Name+")"); err != nil {
			return err
		}

		name, err := debug.Add(cmd.Context(), c, pod.Namespace, pod.Name, debugOpts.image, target)
		if err != nil {
			return fmt.Errorf("add debug container: %w", err)
		}
		fmt.Fprintf(os.Stderr, "kx: debug container %s (%s) added to pod %s, sharing processes with %s; it stays until the pod is replaced\n",
			name, debugOpts.image, pod.Name, target)
		if err := debug.WaitRunning(cmd.Context(), c, pod.Namespace, pod.Name, name, 90*time.Second, 500*time.Millisecond); err != nil {
			return err
		}
		return runInPod(cmd.Context(), c, kxexec.Target{Namespace: pod.Namespace, Pod: pod.Name, Container: name}, args[1:])
	},
}

func init() {
	f := debugCmd.Flags()
	f.StringVar(&debugOpts.image, "image", "busybox:1.36", "image of the debug container")
	f.StringVarP(&debugOpts.target, "container", "c", "", "container to share processes with (default: the pod's default container)")
	f.StringVarP(&debugOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	debugCmd.ValidArgsFunction = complete(listApps)
	rootCmd.AddCommand(debugCmd)
}
