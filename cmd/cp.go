package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/j3yzz/kx/internal/cp"
	kxexec "github.com/j3yzz/kx/internal/exec"
	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/resolver"
	"github.com/spf13/cobra"
)

var cpOpts struct {
	container string
	selector  string
	pick      bool
}

var cpCmd = &cobra.Command{
	Use:   "cp <app>:<path> <local> | <local> <app>:<path>",
	Short: "Copy files between your machine and an app's pod",
	Long: `Copies a file or directory to or from a pod of the app, using tar through the
exec API. The container needs a tar binary. If the destination is an existing
directory the copy lands inside it; otherwise the destination is the new name.`,
	Example: `  kx cp payments:/app/logs/error.log .
  kx cp ./config.json payments:/app/ -n staging`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, dst := args[0], args[1]
		sApp, sPath, sRemote := splitRemote(src)
		dApp, dPath, dRemote := splitRemote(dst)
		if sRemote == dRemote {
			return fmt.Errorf("exactly one side must be <app>:<path> (got %q and %q)", src, dst)
		}
		app, remote, action := sApp, sPath, guard.CopyOut
		if dRemote {
			app, remote, action = dApp, dPath, guard.CopyIn
		}
		if remote == "" {
			return fmt.Errorf("missing path after %q", app+":")
		}
		c, app, err := newAppClient(app)
		if err != nil {
			return err
		}
		if err := requireAccess(c, getPods, listPods, execPods); err != nil {
			return err
		}
		pods, err := resolver.New(c).Resolve(cmd.Context(), c.Namespace, app, cpOpts.selector)
		if err != nil {
			return err
		}
		pod, err := resolver.PickPod(pods, cpOpts.pick, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
		container, err := resolver.PickContainer(pod, cpOpts.container, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
		if err := guardCheck(c, action, app+" ("+pod.Name+")"); err != nil {
			return err
		}
		run := podRunner(c, kxexec.Target{Namespace: pod.Namespace, Pod: pod.Name, Container: container})
		if dRemote {
			err = cp.Upload(cmd.Context(), run, src, remote)
		} else {
			err = cp.Download(cmd.Context(), run, remote, dst)
		}
		return kube.WrapError(err)
	},
}

// splitRemote splits "app:path". A first part containing "/" or "." is a local
// path that merely has a colon in it.
func splitRemote(arg string) (app, path string, remote bool) {
	app, path, ok := strings.Cut(arg, ":")
	if !ok || app == "" || strings.ContainsAny(app, "/.") {
		return "", "", false
	}
	return app, path, true
}

func podRunner(c *kube.Client, t kxexec.Target) cp.Runner {
	return func(ctx context.Context, command []string, in io.Reader, out, errOut io.Writer) error {
		return kxexec.Stream(ctx, c, t, command, in, out, errOut)
	}
}

func init() {
	f := cpCmd.Flags()
	f.StringVarP(&cpOpts.container, "container", "c", "", "container name")
	f.StringVarP(&cpOpts.selector, "selector", "l", "", "label selector to find the app's pods")
	f.BoolVar(&cpOpts.pick, "pick", false, "choose the pod from a list")
	rootCmd.AddCommand(cpCmd)
}
