package cmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/rbac"
	"github.com/j3yzz/kx/internal/workload"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart <app>",
	Short: "Rolling restart of the app's Deployment, StatefulSet or DaemonSet",
	Long: `Restarts the pods one by one, like kubectl rollout restart. The app name must be
the exact workload name; kx does not guess through labels for actions that change the cluster.`,
	Example: `  kx restart payments -n staging`,
	Args:    orPick(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		args, err := pickAppArg(cmd, args)
		if err != nil {
			return err
		}
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		w, err := workload.Find(cmd.Context(), c, c.Namespace, app)
		if err != nil {
			return err
		}
		if err := requireAccess(c, rbac.Req{Verb: "patch", Group: "apps", Resource: w.Resource}); err != nil {
			return err
		}
		if err := guardCheck(c, guard.Restart, w.String()); err != nil {
			return err
		}
		if err := workload.Restart(cmd.Context(), c, c.Namespace, w, time.Now()); err != nil {
			return err
		}
		fmt.Printf("restarting %s\n", w)
		return nil
	},
}

var scaleCmd = &cobra.Command{
	Use:   "scale <app> <replicas>",
	Short: "Set the replica count of the app's Deployment or StatefulSet",
	Long: `Sets the number of replicas. The app name must be the exact workload name.
Scaling to 0 stops the app. DaemonSets cannot be scaled.`,
	Example: `  kx scale payments 3 -n staging
  kx scale worker 0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 0 {
			return fmt.Errorf("replicas must be a number, 0 or more (got %q)", args[1])
		}
		c, app, err := newAppClient(args[0])
		if err != nil {
			return err
		}
		w, err := workload.Find(cmd.Context(), c, c.Namespace, app)
		if err != nil {
			return err
		}
		if err := requireAccess(c, rbac.Req{Verb: "patch", Group: "apps", Resource: w.Resource}); err != nil {
			return err
		}
		if err := guardCheck(c, guard.Scale, fmt.Sprintf("%s to %d replicas (now %d)", w, n, w.Replicas)); err != nil {
			return err
		}
		if err := workload.Scale(cmd.Context(), c, c.Namespace, w, int32(n)); err != nil {
			return err
		}
		fmt.Printf("scaled %s from %d to %d\n", w, w.Replicas, n)
		return nil
	},
}

func init() {
	restartCmd.ValidArgsFunction = complete(listApps)
	scaleCmd.ValidArgsFunction = complete(listApps)
	rootCmd.AddCommand(restartCmd, scaleCmd)
}
