// Package tui holds small shared terminal UI helpers used across pmox
// commands. Everything here is a thin wrapper around charmbracelet/huh,
// deliberately kept narrow so callers don't reach for huh directly.
package tui

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/charmbracelet/huh"
)

// SelectOne runs a huh.Select with arrow-key navigation (no filter input).
// Returns the chosen value, or the fallback on error. On user-abort
// (Ctrl+C / Esc) it re-raises SIGINT so the root signal handler cancels the
// process context — huh/bubbletea traps the signal itself, so we have to
// synthesise it for our own code.
func SelectOne(title string, opts []huh.Option[string], fallback string) string {
	if len(opts) == 0 {
		return fallback
	}
	if len(opts) == 1 {
		return opts[0].Value
	}
	fmt.Println()
	selected := opts[0].Value
	err := huh.NewSelect[string]().
		Title(title).
		Options(opts...).
		Value(&selected).
		Filtering(false).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		}
		return fallback
	}
	return selected
}
