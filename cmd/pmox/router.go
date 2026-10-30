package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pvessh"
	"github.com/eugenetaranov/pmox/internal/router"
	"github.com/eugenetaranov/pmox/internal/server"
	"github.com/eugenetaranov/pmox/internal/tui"
)

func newRouterCmd() *cobra.Command {
	return nounGroup("router", "Reach the Proxmox node from away (Tailscale router)", `Manage pmox's overlay router: a small container on the node's bridge
that runs Tailscale and advertises the node's own address (a /32), so
the Proxmox API and node SSH work away from home.

The container is unprivileged, runs Tailscale in userspace mode, has no
SSH server, and is managed with 'pct exec' over node SSH, so node SSH
must be configured. Auth keys are never stored. See
docs/remote-access.md.`,
		newRouterCreateCmd(), newRouterLoginCmd(), newRouterStatusCmd(), newRouterRouteCmd(),
		rename(newRouterDeleteCmd(), "delete", "rm"))
}

// routerEnv is what a router command needs: the API, the node runner
// (node SSH, dialled on first use) and where the node is.
type routerEnv struct {
	api      router.API
	run      router.Runner
	resolved *server.Resolved
	node     string
	bridge   string
	close    func()
}

// routerEnvFn opens a routerEnv. A seam for tests.
var routerEnvFn = func(ctx context.Context, cmd *cobra.Command) (*routerEnv, error) {
	client, resolved, err := buildClient(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if !resolved.HasNodeSSH() {
		return nil, fmt.Errorf("%w: the router is managed with 'pct exec' over node SSH, which isn't configured for this server; set it up with: pmox init", exitcode.ErrUserInput)
	}
	srv := resolved.Server
	if srv.Node == "" {
		return nil, fmt.Errorf("%w: no node configured; run 'pmox init'", exitcode.ErrNotFound)
	}
	var conn *pvessh.Client
	env := &routerEnv{
		api:      client,
		resolved: resolved,
		node:     srv.Node,
		bridge:   firstNonEmpty(srv.Bridge, "vmbr0"),
	}
	env.run = func(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error {
		if conn == nil {
			c, err := dialPvessh(ctx, resolved)
			if err != nil {
				return fmt.Errorf("node SSH: %w", err)
			}
			conn = c
		}
		return conn.Run(ctx, command, stdin, stdout, stderr)
	}
	env.close = func() {
		if conn != nil {
			_ = conn.Close()
		}
	}
	return env, nil
}

// nodeRoute is the node's own /32 for env.
func (e *routerEnv) nodeRoute(ctx context.Context) (string, error) {
	return router.NodeRoute(ctx, e.api, e.node, e.bridge, e.resolved.URL, net.DefaultResolver.LookupHost)
}

type routerCreateFlags struct {
	keyFile  string
	routes   []string
	ip, gw   string
	firewall bool
}

func newRouterCreateCmd() *cobra.Command {
	f := &routerCreateFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create the router and join it to your tailnet",
		Long: `Create the router container on the node's bridge and join it to your
tailnet. It advertises the node's own address as a /32 unless --route
says otherwise.

The Tailscale auth key comes from --auth-key-file, TS_AUTHKEY, or a
prompt; pmox passes it into the container on stdin and keeps no copy.
Recommended key: pre-approved, single-use, not ephemeral, tagged (a
tagged device's key doesn't expire). Without a key, the router is
created and 'pmox router login' joins it with a login URL.

--firewall turns on the container's guest firewall (outbound only to
the routed hosts, Tailscale, DNS and the web). It needs --ip, and the
datacenter firewall already on; pmox never enables that itself.`,
		Example: `  pmox router create --auth-key-file ~/ts.key
  TS_AUTHKEY=tskey-auth-… pmox router create
  pmox router create --route 192.168.0.185/32 --route 192.168.0.171/32
  pmox router create --firewall --ip 192.168.0.250/24 --gw 192.168.0.1`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runRouterCreate(cmd, f) },
	}
	cmd.Flags().StringVar(&f.keyFile, "auth-key-file", "", "file holding the Tailscale auth key (env: TS_AUTHKEY)")
	cmd.Flags().StringArrayVar(&f.routes, "route", nil, "route to advertise (repeatable; default: the node's own /32)")
	cmd.Flags().StringVar(&f.ip, "ip", "", "static address for the container, as a CIDR (default: DHCP)")
	cmd.Flags().StringVar(&f.gw, "gw", "", "gateway for --ip")
	cmd.Flags().BoolVar(&f.firewall, "firewall", false, "turn on the container's guest firewall (needs --ip and the datacenter firewall)")
	return cmd
}

func runRouterCreate(cmd *cobra.Command, f *routerCreateFlags) error {
	ctx := cmd.Context()
	if f.ip != "" {
		if _, _, err := net.ParseCIDR(f.ip); err != nil {
			return fmt.Errorf("%w: --ip %q: want a CIDR such as 192.168.0.250/24", exitcode.ErrUserInput, f.ip)
		}
	}
	env, err := routerEnvFn(ctx, cmd)
	if err != nil {
		return err
	}
	defer env.close()

	routes := f.routes
	if len(routes) == 0 {
		r, err := env.nodeRoute(ctx)
		if err != nil {
			return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
		}
		routes = []string{r}
	}
	for _, r := range routes {
		if err := router.CheckRoute(r); err != nil {
			return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
		}
	}
	// Read the key before building anything, so a prompt comes first.
	key, err := readAuthKey(cmd, f.keyFile)
	if err != nil {
		return err
	}

	sp := startSpin("Creating the router…")
	vmid, err := router.Create(ctx, env.api, env.run, router.CreateOptions{
		Node: env.node, Bridge: env.bridge, Storage: env.resolved.Server.Storage,
		Routes: routes, IP: f.ip, Gateway: f.gw, Firewall: f.firewall,
		Progress: func(s string) { progress(cmd, sp, s) },
	})
	sp.Stop()
	if err != nil {
		if vmid != 0 {
			return fmt.Errorf("%w (container %d is left for inspection; remove it with: pmox router delete)", err, vmid)
		}
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Created router container %d on %s, advertising %s.\n", vmid, env.node, strings.Join(routes, ", "))
	if len(key) == 0 {
		fmt.Fprintln(out, "Not joined to a tailnet yet: run 'pmox router login' (it shows a login URL), or create with an auth key.")
		return nil
	}
	sp = startSpin("Joining the tailnet…")
	err = router.Join(ctx, env.run, vmid, env.node, routes, key, nil)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("%w (the container is ready; try again with: pmox router login)", err)
	}
	fmt.Fprintf(out, "Joined the tailnet as %s.\n", router.Hostname(env.node))
	printRouterNext(ctx, out, env, vmid)
	return nil
}

// printRouterNext says what's left in the Tailscale admin console:
// approving routes, and key expiry on an untagged router.
func printRouterNext(ctx context.Context, w io.Writer, env *routerEnv, vmid int) {
	st, err := router.State(ctx, env.run, vmid)
	if err != nil {
		return
	}
	if waiting := st.Awaiting(); len(waiting) > 0 {
		fmt.Fprintf(w, "Approve %s in the Tailscale admin console (Machines → %s → Edit route settings),\n", strings.Join(waiting, ", "), router.Hostname(env.node))
		fmt.Fprintln(w, `or let the policy approve them: "autoApprovers": {"routes": {"<your LAN>/24": ["<you or tag:pmox-router>"]}}.`)
	}
	if !st.Tagged {
		fmt.Fprintf(w, "The router isn't tagged, so its key expires: disable key expiry for %s in the admin console.\n", router.Hostname(env.node))
	}
}

// readAuthKey reads the auth key from file, then TS_AUTHKEY, then a
// hidden prompt on a terminal. Nil (no error) means no key.
func readAuthKey(cmd *cobra.Command, file string) ([]byte, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("%w: read --auth-key-file: %w", exitcode.ErrUserInput, err)
		}
		return []byte(strings.TrimSpace(string(b))), nil
	}
	if k := strings.TrimSpace(os.Getenv("TS_AUTHKEY")); k != "" {
		return []byte(k), nil
	}
	if !tui.Interactive() {
		return nil, nil
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Tailscale auth key (leave empty to log in with a URL later): ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return nil, fmt.Errorf("%w: read the auth key: %w", exitcode.ErrUserInput, err)
	}
	return []byte(strings.TrimSpace(string(b))), nil
}

func newRouterLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Join the router to your tailnet with a login URL",
		Long: `Log the router in to Tailscale without an auth key: Tailscale prints a
login URL; open it, approve the device, and the command returns. It keeps
the routes the router advertises (the node's /32 by default).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env, err := routerEnvFn(ctx, cmd)
			if err != nil {
				return err
			}
			defer env.close()
			r, err := router.Find(ctx, env.api)
			if err != nil {
				return routerErr(err)
			}
			var routes []string
			if st, err := router.State(ctx, env.run, r.VMID); err == nil && len(st.Advertised) > 0 {
				routes = st.Advertised
			} else if nr, err := env.nodeRoute(ctx); err == nil {
				routes = []string{nr}
			} else {
				return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Waiting for the login URL…")
			if err := router.Login(ctx, env.run, r.VMID, env.node, routes, cmd.ErrOrStderr()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "The router is on your tailnet as %s.\n", router.Hostname(env.node))
			printRouterNext(ctx, cmd.OutOrStdout(), env, r.VMID)
			return nil
		},
	}
}

func newRouterStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the router: container, tailnet, routes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env, err := routerEnvFn(ctx, cmd)
			if err != nil {
				return err
			}
			defer env.close()
			sp := startSpin("Checking the router…")
			st, err := router.Report(ctx, env.api, env.run, router.StatusOptions{
				Bridge: env.bridge, APIURL: env.resolved.URL, Lookup: net.DefaultResolver.LookupHost,
			})
			sp.Stop()
			if err != nil {
				return routerErr(err)
			}
			if outputMode == "json" {
				return printJSON(cmd.OutOrStdout(), st)
			}
			renderRouterStatus(cmd.OutOrStdout(), st)
			return nil
		},
	}
}

func renderRouterStatus(w io.Writer, st *router.Status) {
	yes := map[bool]string{true: "yes", false: "no"}
	fmt.Fprintf(w, "Container:  %d on %s, %s", st.VMID, st.Node, st.State)
	if st.Address != "" {
		fmt.Fprintf(w, " (%s)", st.Address)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Starts:     on boot %s, %s; protected %s\n", yes[st.OnBoot], firstNonEmpty(st.Startup, "no startup order"), yes[st.Protected])
	fmt.Fprintf(w, "Egress:     %s\n", st.Egress)
	if st.Tailscale == nil {
		fmt.Fprintf(w, "Tailscale:  unknown (%s)\n", st.TailscaleErr)
		return
	}
	ts := st.Tailscale
	fmt.Fprintf(w, "Tailscale:  %s, %s", ts.Backend, firstNonEmpty(ts.IP, "no address"))
	if !ts.Tagged {
		fmt.Fprint(w, ", untagged (disable its key expiry in the admin console)")
	}
	fmt.Fprintln(w)
	renderRoutes(w, ts)
	switch {
	case st.NodeRoute == "":
		fmt.Fprintln(w, "Node:       can't tell the node's address")
	case !st.RouteFresh:
		fmt.Fprintf(w, "Node:       %s is NOT advertised: the node's address changed? fix with: pmox router route add %s\n", st.NodeRoute, st.NodeRoute)
	default:
		fmt.Fprintf(w, "Node:       %s advertised\n", st.NodeRoute)
	}
}

func renderRoutes(w io.Writer, ts *router.TailscaleState) {
	if len(ts.Advertised) == 0 {
		fmt.Fprintln(w, "Routes:     none advertised")
		return
	}
	width := 0
	for _, r := range ts.Advertised {
		width = max(width, len(r))
	}
	for i, r := range ts.Advertised {
		label := ""
		if i == 0 {
			label = "Routes:"
		}
		state := "approved"
		if slices.Contains(ts.Awaiting(), r) {
			state = "awaiting approval in the Tailscale admin console"
		}
		fmt.Fprintf(w, "%-12s%-*s  %s\n", label, width, r, state)
	}
}

func newRouterRouteCmd() *cobra.Command {
	var force bool
	add := &cobra.Command{
		Use:   "add <cidr>...",
		Short: "Advertise more routes through the router",
		Long: `Add routes the router advertises, such as a VM's address
(192.168.0.171/32). Routes must be in a private range and no wider than
/16. A new route needs approval in the Tailscale admin console unless an
autoApprovers entry in your policy covers it.`,
		Example: `  pmox router route add 192.168.0.171/32`,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRouteChange(cmd, args, true, false)
		},
	}
	rm := &cobra.Command{
		Use:   "rm <cidr>...",
		Short: "Stop advertising routes",
		Long: `Remove routes the router advertises. Removing the node's own /32 needs
--force: without it, pmox can't reach the API away from home.`,
		Aliases: []string{"delete"},
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRouteChange(cmd, args, false, force)
		},
	}
	rm.Flags().BoolVar(&force, "force", false, "allow removing the node's own route")
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List advertised routes and whether they're approved",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env, err := routerEnvFn(ctx, cmd)
			if err != nil {
				return err
			}
			defer env.close()
			r, err := router.Find(ctx, env.api)
			if err != nil {
				return routerErr(err)
			}
			st, err := router.State(ctx, env.run, r.VMID)
			if err != nil {
				return err
			}
			if outputMode == "json" {
				return printJSON(cmd.OutOrStdout(), map[string]any{"advertised": st.Advertised, "approved": st.Approved, "awaiting": st.Awaiting()})
			}
			renderRoutes(cmd.OutOrStdout(), &st)
			return nil
		},
	}
	return nounGroup("route", "Change the routes the router advertises", `Add, remove or list the routes the router advertises.`, add, rm, list)
}

// runRouteChange adds (add) or removes the routes in args. Without args
// it asks on a terminal: a CIDR to add, or which advertised routes to
// remove.
func runRouteChange(cmd *cobra.Command, args []string, add, force bool) error {
	ctx := cmd.Context()
	if len(args) == 0 && !tui.Interactive() {
		return fmt.Errorf("%w: pass one or more routes, e.g. %s 192.168.0.171/32", exitcode.ErrUserInput, cmd.CommandPath())
	}
	env, err := routerEnvFn(ctx, cmd)
	if err != nil {
		return err
	}
	defer env.close()
	if len(args) == 0 {
		if args, err = askRoutes(ctx, env, add); err != nil {
			return err
		}
	}
	var toAdd, toRemove []string
	if add {
		toAdd = args
	} else {
		toRemove = args
	}
	nodeRoute, _ := env.nodeRoute(ctx)
	routes, err := router.ChangeRoutes(ctx, env.api, env.run, toAdd, toRemove, nodeRoute, force)
	if err != nil {
		if errors.Is(err, router.ErrNotFound) {
			return routerErr(err)
		}
		return fmt.Errorf("%w: %w", exitcode.ErrUserInput, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "The router now advertises: %s\n", strings.Join(routes, ", "))
	if add {
		fmt.Fprintln(cmd.OutOrStdout(), "New routes need approval in the Tailscale admin console unless an autoApprovers entry covers them (check: pmox router route list).")
	}
	return nil
}

// askRoutes asks for a route to add, or which advertised routes to
// remove.
func askRoutes(ctx context.Context, env *routerEnv, add bool) ([]string, error) {
	if add {
		r, err := askTextFn("Route to advertise (e.g. 192.168.0.171/32)")
		if err != nil {
			return nil, tui.AbortErr(err)
		}
		return []string{strings.TrimSpace(r)}, nil
	}
	r, err := router.Find(ctx, env.api)
	if err != nil {
		return nil, routerErr(err)
	}
	st, err := router.State(ctx, env.run, r.VMID)
	if err != nil {
		return nil, err
	}
	if len(st.Advertised) == 0 {
		return nil, fmt.Errorf("%w: the router advertises no routes", exitcode.ErrUserInput)
	}
	opts := make([]huh.Option[string], len(st.Advertised))
	for i, a := range st.Advertised {
		opts[i] = huh.NewOption(a, a)
	}
	picked, err := tui.SelectMulti("Routes to stop advertising", opts)
	if err != nil {
		return nil, tui.AbortErr(err)
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("%w: nothing picked", exitcode.ErrUserInput)
	}
	return picked, nil
}

type routerDeleteFlags struct{ yes bool }

func newRouterDeleteCmd() *cobra.Command {
	f := &routerDeleteFlags{}
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Log the router out and destroy its container",
		Long: `Log the router out of Tailscale, then destroy its container and disks.
Logging out doesn't remove the device from your tailnet: remove it in the
Tailscale admin console afterwards, or the next router joins under a
suffixed name.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runRouterDelete(cmd, f) },
	}
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "skip the confirmation prompt (env: PMOX_ASSUME_YES)")
	return cmd
}

func runRouterDelete(cmd *cobra.Command, f *routerDeleteFlags) error {
	ctx := cmd.Context()
	assumeYes := f.yes || envBool("PMOX_ASSUME_YES")
	if !assumeYes && !tui.StdinIsTerminal() {
		return fmt.Errorf("%w: refusing to delete the router without a terminal; pass --yes", exitcode.ErrUserInput)
	}
	env, err := routerEnvFn(ctx, cmd)
	if err != nil {
		return err
	}
	defer env.close()
	r, err := router.Find(ctx, env.api)
	if err != nil {
		return routerErr(err)
	}
	if !assumeYes {
		ok, err := tui.NewTTYConfirmer(os.Stdin, cmd.ErrOrStderr()).Confirm(ctx,
			fmt.Sprintf("Delete the router (container %d on %s)? Away from home, pmox loses the node until you create another", r.VMID, r.Node))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: cancelled", exitcode.ErrUserInput)
		}
	}
	sp := startSpin("Deleting the router…")
	vmid, node, logoutErr, err := router.Delete(ctx, env.api, env.run)
	sp.Stop()
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if logoutErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", tui.Warnf(fmt.Sprintf("warning: couldn't log the router out (%v)", logoutErr)))
	}
	fmt.Fprintf(w, "Destroyed router container %d on %s.\n", vmid, node)
	fmt.Fprintf(w, "Remove the device %s in the Tailscale admin console (Machines), or the next router joins as %s-1.\n", router.Hostname(node), router.Hostname(node))
	return nil
}

// routerErr maps "no router" to a not-found exit.
func routerErr(err error) error {
	if errors.Is(err, router.ErrNotFound) {
		return fmt.Errorf("%w: %w", exitcode.ErrNotFound, err)
	}
	return err
}
