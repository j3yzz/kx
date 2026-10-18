package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/j3yzz/kx/internal/config"
	"github.com/j3yzz/kx/internal/forward"
	"github.com/spf13/cobra"
)

var tunnelCmd = &cobra.Command{
	Use:     "tunnel [profile]",
	Aliases: []string{"tun"},
	Short:   "Open a saved port-forward profile, optionally launching a local client",
	Long: `Profiles live in ~/.config/kx/config.yaml:

  tunnels:
    db:
      app: mysql
      namespace: data              # optional
      ports: ["13306:3306"]
      client: mysql -h {host} -P {port} -u root -p   # optional

With a client, kx opens the tunnel, runs the client in your terminal, and closes
the tunnel when the client exits (kx exits with the client's exit code).
Without a client it behaves like 'kx forward'. With no profile name it lists them.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return listTunnels(cfg.Tunnels)
		}
		t, ok := cfg.Tunnels[args[0]]
		if !ok {
			return fmt.Errorf("no tunnel profile %q (see 'kx tunnel' for the list)", args[0])
		}
		c, err := newClientIn(t.Namespace)
		if err != nil {
			return err
		}

		var clientErr error
		var onReady func([]forward.PortSpec, func())
		if t.Client != "" {
			onReady = func(ports []forward.PortSpec, stop func()) {
				go func() {
					defer stop() // client done: close the tunnel
					clientErr = runClient(t.Client, forwardOpts.address, ports[0].Local)
				}()
			}
		}
		if err := startForward(cmd.Context(), c, t.App, "", forwardOpts.address, t.Ports, onReady); err != nil {
			return err
		}
		return clientErr
	},
}

// clientCommand substitutes {host} and {port} (the first local port).
func clientCommand(tmpl, host string, port int) string {
	return strings.NewReplacer("{host}", host, "{port}", strconv.Itoa(port)).Replace(tmpl)
}

// runClient runs the client in the foreground; its exit code becomes ours.
func runClient(tmpl, host string, port int) error {
	cmd := exec.Command("sh", "-c", clientCommand(tmpl, host, port))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &CodeError{Code: ee.ExitCode()}
	}
	return err
}

func listTunnels(ts map[string]config.Tunnel) error {
	if len(ts) == 0 {
		fmt.Fprintln(os.Stderr, "no tunnel profiles; add some under 'tunnels:' in ~/.config/kx/config.yaml (see 'kx tunnel --help')")
		return nil
	}
	names := make([]string, 0, len(ts))
	for n := range ts {
		names = append(names, n)
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROFILE\tAPP\tNAMESPACE\tPORTS\tCLIENT")
	for _, n := range names {
		t := ts[n]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", n, t.App, t.Namespace, strings.Join(t.Ports, ","), t.Client)
	}
	return tw.Flush()
}

func init() {
	tunnelCmd.ValidArgsFunction = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		cfg, err := loadConfig()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := make([]string, 0, len(cfg.Tunnels))
		for n := range cfg.Tunnels {
			names = append(names, n)
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
	rootCmd.AddCommand(tunnelCmd)
}
