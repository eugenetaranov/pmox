// Package main is the entrypoint for the pmox CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var (
	debug      bool
	verbose    bool
	noColor    bool
	outputMode string
	serverFlag string
)

var rootCmd = &cobra.Command{
	Use:   "pmox",
	Short: "pmox - multipass-style CLI for Proxmox VE",
	Long: `pmox is a command-line tool for launching and managing VMs on Proxmox VE,
inspired by Canonical's multipass.

Run ` + "`pmox --help`" + ` to see available commands.`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
	// Runtime errors should not print the usage block; usage is only for
	// flag/argument parsing errors, which cobra still shows because those
	// happen before RunE executes. Errors are printed by main() so we can
	// suppress noise on Ctrl+C.
	SilenceUsage:  true,
	SilenceErrors: true,
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
	// PMOX_SERVER. `pmox configure` ignores both the flag and the env var.
	rootCmd.PersistentFlags().StringVar(&serverFlag, "server", "", "Proxmox server URL (overrides PMOX_SERVER)")

	rootCmd.AddCommand(versionCmd)
}

// signalContext returns a context that is cancelled on the first SIGINT/SIGTERM
// and exits the process on the second signal. Interactive prompts should wrap
// os.Stdin with a cancelreader that observes this context, so blocked reads
// unblock cleanly when the first signal arrives.
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
		// printed "Interrupted, cleaning up..."; skip the duplicate error line.
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}
	os.Exit(exitcode.From(err))
}
