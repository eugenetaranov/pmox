// Package main is the entrypoint for the pmox CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// selfReporter is implemented by errors that have already rendered their
// own user-facing output (e.g. `pmox doctor`, which prints a full report
// and only returns an error to carry the exit code). main skips the
// generic "Error: ..." line for these. It is deliberately distinct from
// exitcode.Coder: carrying an exit code does not imply the error was
// already printed (e.g. hook failures).
type selfReporter interface {
	SelfReported()
}

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var (
	debug       bool
	verbose     bool
	noColor     bool
	outputMode  string
	serverFlag  string
	contextFlag string
	sshInsecure bool
	noInput     bool
)

// sshInsecureWarned tracks whether we've emitted the stderr warning for
// --ssh-insecure yet in this process. Reset per-process; flipped once
// on first actual use.
var sshInsecureWarned bool

// SSHInsecure returns true if the operator asked to skip SSH host-key
// verification via --ssh-insecure or PMOX_SSH_INSECURE=1. First call
// emits a one-shot warning on stderr so the risk is visible in logs.
func SSHInsecure() bool {
	if !sshInsecure {
		return false
	}
	if !sshInsecureWarned {
		sshInsecureWarned = true
		fmt.Fprintln(os.Stderr, "WARNING: --ssh-insecure active — SSH host-key verification is disabled for this process.")
	}
	return true
}

var rootCmd = &cobra.Command{
	Use:   "pmox",
	Short: "pmox - multipass-style CLI for Proxmox VE",
	Long: `pmox is a command-line tool for launching and managing VMs on Proxmox VE,
inspired by Canonical's multipass.

Run ` + "`pmox --help`" + ` to see available commands. On a terminal, running
'pmox' with no command shows an interactive picker instead.`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
	// Runtime errors should not print the usage block; usage is only for
	// flag/argument parsing errors, which cobra still shows because those
	// happen before RunE executes. Errors are printed by main() so we can
	// suppress noise on Ctrl+C.
	SilenceUsage:  true,
	SilenceErrors: true,
	// Resolve the interactive-input policy once, before any command runs:
	// --no-input, PMOX_NO_INPUT, and --output json all disable prompts so
	// pmox stays script/CI-safe and never corrupts JSON on stdout.
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		tui.SetNoInput(noInput || envBool("PMOX_NO_INPUT") || outputMode == "json")
	},
	// With RunE set, cobra routes a bare 'pmox' (no positional args —
	// any subcommand-shaped input still resolves to that subcommand and
	// never reaches this) here instead of printing help. Non-interactively
	// (no TTY, --no-input, --output json) fall back to the exact old
	// behavior: print help, no error.
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !tui.Interactive() {
			return cmd.Help()
		}
		return runRootMenu(cmd)
	},
}

// runRootMenu shows a top-level command picker (arrow keys, or type to
// filter) and, once one is chosen, runs it exactly as if it had been
// typed with no further arguments — so each command's own interactive
// prompting (a VM picker, launch's name/sizing prompts, etc.) takes over
// from there.
func runRootMenu(cmd *cobra.Command) error {
	root := cmd.Root()
	opts := rootMenuOptions(root)
	chosen, err := tui.Select("What would you like to do?", opts)
	if err != nil {
		return err
	}
	root.SetArgs([]string{chosen})
	return root.ExecuteContext(cmd.Context())
}

// rootMenuOptions lists root's runnable subcommands in the same order
// they appear under `pmox --help`: grouped (lifecycle, then access, then
// setup — see addGrouped), then ungrouped commands (version) last.
// cobra keeps Commands() sorted alphabetically by name, so each group's
// commands come out alphabetically too, matching --help exactly. The
// hidden 'configure' shim and cobra's own 'help'/'completion' commands
// are noise in a picker and are skipped.
func rootMenuOptions(root *cobra.Command) []huh.Option[string] {
	groupOrder := []string{groupLifecycle, groupAccess, groupSetup, ""}
	byGroup := make(map[string][]*cobra.Command, len(groupOrder))
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		byGroup[c.GroupID] = append(byGroup[c.GroupID], c)
	}
	var opts []huh.Option[string]
	for _, g := range groupOrder {
		for _, c := range byGroup[g] {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%-15s %s", c.Name(), c.Short), c.Name()))
		}
	}
	return opts
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the pmox version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("pmox version %s (commit: %s, built: %s)\n", version, commit, date)
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug output with detailed information")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")
	rootCmd.PersistentFlags().StringVar(&outputMode, "output", "text", "Output format: text or json")
	// --server selects which configured server a command targets. Overrides
	// PMOX_SERVER. `pmox init` ignores both the flag and the env var.
	rootCmd.PersistentFlags().StringVar(&serverFlag, "server", "", "Server context name or URL to target (overrides PMOX_SERVER)")
	rootCmd.PersistentFlags().StringVar(&contextFlag, "context", "", "Context (configured server) to target by name (env: PMOX_CONTEXT)")
	rootCmd.PersistentFlags().BoolVar(&sshInsecure, "ssh-insecure", envBool("PMOX_SSH_INSECURE"), "Skip SSH host-key verification (env: PMOX_SSH_INSECURE)")
	rootCmd.PersistentFlags().BoolVar(&noInput, "no-input", false, "Never prompt; error instead of showing an interactive picker (env: PMOX_NO_INPUT)")

	// Group commands so `pmox --help` reads as labeled sections instead of
	// one flat wall. Grouping is help-presentation only — every command is
	// still invoked exactly as before (e.g. `pmox launch web1`).
	rootCmd.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "VM lifecycle:"},
		&cobra.Group{ID: groupAccess, Title: "Access & files:"},
		&cobra.Group{ID: groupSetup, Title: "Setup & diagnostics:"},
	)

	addGrouped(groupLifecycle,
		newLaunchCmd(), newCloneCmd(), newStartCmd(), newStopCmd(),
		newDeleteCmd(), newListCmd(), newInfoCmd(),
	)
	addGrouped(groupAccess,
		newShellCmd(), newExecCmd(), newApplyCmd(), newCpCmd(), newSyncCmd(),
		newMountCmd(), newUmountCmd(), newSSHConfigCmd(),
	)
	// initCmd is declared in init.go; register + group it here so
	// all command registration lives in one place.
	addGrouped(groupSetup, initCmd, newConfigCmd(), newCreateTemplateCmd(), newDoctorCmd(), newCleanupCmd())

	// version stays ungrouped and lands under cobra's "Additional Commands"
	// alongside the built-in help/completion.
	rootCmd.AddCommand(versionCmd)

	// 'configure' was renamed to 'init'. Keep a hidden stub that points
	// users at the new name instead of cobra's generic "unknown command".
	rootCmd.AddCommand(deprecatedConfigureCmd())
}

// deprecatedConfigureCmd is a hidden placeholder for the old 'configure'
// command name. It does nothing but tell the user to use 'pmox init'.
func deprecatedConfigureCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "configure",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("%w: 'pmox configure' was renamed to 'pmox init'", exitcode.ErrUserInput)
		},
	}
}

const (
	groupLifecycle = "lifecycle"
	groupAccess    = "access"
	groupSetup     = "setup"
)

// addGrouped assigns a help GroupID to each command and registers it on
// the root. GroupID affects only how `--help` is sectioned, not how the
// command is invoked.
func addGrouped(group string, cmds ...*cobra.Command) {
	for _, c := range cmds {
		c.GroupID = group
		rootCmd.AddCommand(c)
	}
}

// signalContext returns a context that is cancelled on the first SIGINT/SIGTERM
// and exits the process on the second signal. Interactive prompts should wrap
// os.Stdin with a cancelreader that observes this context, so blocked reads
// unblock cleanly when the first signal arrives.
func envBool(name string) bool {
	v := os.Getenv(name)
	return v == "1" || v == "true" || v == "TRUE" || v == "yes"
}

// exactArgs requires exactly n positional arguments and, on mismatch,
// returns an example-driven error instead of cobra's terse "accepts N
// arg(s), received M". Arguments after a literal "--" are not counted,
// so commands can accept a fixed number of positionals plus pass-through
// flags (e.g. `pmox cp ./a web1:/tmp -- -l 1000`).
func exactArgs(n int, usage, example string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		got := len(args)
		if d := cmd.ArgsLenAtDash(); d >= 0 {
			got = d
		}
		if got == n {
			return nil
		}
		return fmt.Errorf("expected %d argument(s), got %d — usage: %s (example: %s)", n, got, usage, example)
	}
}

// zeroOrExactArgs accepts either zero positional arguments (the command
// then prompts for all of them interactively) or exactly n. Any other
// count — most commonly a partial set that would leave the remaining
// ones ambiguous — is rejected with the same example-driven error
// exactArgs uses. Arguments after a literal "--" are not counted, same
// as exactArgs.
func zeroOrExactArgs(n int, usage, example string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		got := len(args)
		if d := cmd.ArgsLenAtDash(); d >= 0 {
			got = d
		}
		if got == 0 || got == n {
			return nil
		}
		return fmt.Errorf("expected 0 or %d argument(s), got %d — usage: %s (example: %s)", n, got, usage, example)
	}
}

func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintln(os.Stderr, "\nInterrupted.")
			stop()
			<-sigCh
			os.Exit(130)
		case <-ctx.Done():
		}
	}()
	return ctx, stop
}

func main() {
	ctx, cancel := signalContext(context.Background())
	defer cancel()
	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		// If the context was cancelled (Ctrl+C), the signal handler already
		// printed "Interrupted."; skip the duplicate error line. An aborted
		// interactive prompt gets the same message instead of "Error: ...".
		// Also skip errors that already rendered their own output.
		var self selfReporter
		switch {
		case ctx.Err() != nil, errors.As(err, &self):
		case errors.Is(err, tui.ErrAborted):
			fmt.Fprintln(os.Stderr, "Interrupted.")
		default:
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}
	os.Exit(exitcode.From(err))
}
