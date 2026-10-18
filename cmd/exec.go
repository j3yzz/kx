package cmd

import (
	"errors"
	"fmt"
	"os"

	kxexec "github.com/j3yzz/kx/internal/exec"
	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
	utilexec "k8s.io/client-go/util/exec"
)

var execOpts struct {
	container string
	selector  string
	pick      bool
}

var execCmd = &cobra.Command{
	Use:     "exec <app> [-c container] [-- cmd...]",
	Aliases: []string{"sh"},
	Short:   "Open a shell (or run a command) in an app's pod",
	Long: `Finds a Running pod of the app and runs cmd in it. With no cmd it starts bash,
or sh if the container has no bash. The command's exit code becomes kx's exit code.`,
	Example: `  kx exec payments
  kx exec payments -n prod -- ls /app
  kx exec payments -c sidecar --pick`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return errors.New("app name required")
		}
		if d := cmd.ArgsLenAtDash(); (d == -1 && len(args) != 1) || (d != -1 && d != 1) {
			return errors.New("expected: kx exec <app> [-- cmd...]; put the command after --")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, args[0], execOpts.selector)
		if err != nil {
			return err
		}
		pod, err := resolver.PickPod(pods, execOpts.pick, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
		container, err := resolver.PickContainer(pod, execOpts.container, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
		if kube.IsTTY(os.Stderr) {
			fmt.Fprintf(os.Stderr, "kx: pod %s, container %s\n", pod.Name, container)
		}

		err = kxexec.Run(cmd.Context(), c, kxexec.Target{Namespace: pod.Namespace, Pod: pod.Name, Container: container}, args[1:])
		var ee utilexec.ExitError
		if errors.As(err, &ee) {
			return &CodeError{Code: ee.ExitStatus()} // silent: the remote command already said what it had to
		}
		return kube.WrapError(err)
	},
}

func init() {
	f := execCmd.Flags()
	f.StringVarP(&execOpts.container, "container", "c", "", "container name")
	f.StringVarP(&execOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	f.BoolVar(&execOpts.pick, "pick", false, "choose the pod from a list")
	rootCmd.AddCommand(execCmd)
}
