package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	kxexec "github.com/j3yzz/kx/internal/exec"
	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/j3yzz/kx/internal/runall"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
)

var runAllOpts struct {
	container string
	selector  string
}

var runAllCmd = &cobra.Command{
	Use:   "run-all <app> -- cmd...",
	Short: "Run a command in every running pod of an app",
	Long: `Runs cmd in all Running pods of the app at once. Each output line is prefixed
with the pod name. Exits 0 only if the command succeeded everywhere; otherwise it
lists the failed pods and exits 1.`,
	Example: `  kx run-all payments -- date
  kx run-all payments -n prod -y -- cat /etc/hostname`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := appThenCommand(cmd, args); err != nil {
			return err
		}
		if len(args) < 2 {
			return errors.New("expected: kx run-all <app> -- cmd...")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		if err := requireAccess(c, getPods, listPods, execPods); err != nil {
			return err
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, app, runAllOpts.selector)
		if err != nil {
			return err
		}
		var jobs []runall.Job
		for i := range pods {
			pod := &pods[i]
			if pod.Status.Phase != corev1.PodRunning {
				fmt.Fprintf(os.Stderr, "kx: skipping %s (%s)\n", pod.Name, pod.Status.Phase)
				continue
			}
			container, err := resolver.PickContainer(pod, runAllOpts.container, strings.NewReader(""), io.Discard)
			if err != nil {
				return err
			}
			t := kxexec.Target{Namespace: pod.Namespace, Pod: pod.Name, Container: container}
			jobs = append(jobs, runall.Job{Name: pod.Name, Run: func(ctx context.Context, out, errOut io.Writer) error {
				return kxexec.Stream(ctx, c, t, args[1:], nil, out, errOut)
			}})
		}
		if len(jobs) == 0 {
			return fmt.Errorf("no running pods for %s", app)
		}
		if err := guardCheck(c, guard.Exec, fmt.Sprintf("%s (%d pods)", app, len(jobs))); err != nil {
			return err
		}
		failed := 0
		for _, r := range runall.Do(cmd.Context(), jobs, os.Stdout, os.Stderr) {
			if r.Err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "kx: %s: %v\n", r.Name, r.Err)
			}
		}
		if failed > 0 {
			fmt.Fprintf(os.Stderr, "kx: failed in %d of %d pods\n", failed, len(jobs))
			return &CodeError{Code: 1}
		}
		return nil
	},
}

func init() {
	f := runAllCmd.Flags()
	f.StringVarP(&runAllOpts.container, "container", "c", "", "container name")
	f.StringVarP(&runAllOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	runAllCmd.ValidArgsFunction = complete(listApps)
	rootCmd.AddCommand(runAllCmd)
}
