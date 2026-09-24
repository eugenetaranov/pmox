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

// ErrCancelled is returned by pickers when the user aborts (Esc/Ctrl-C).
var ErrCancelled = errors.New("selection cancelled")

// filterThreshold is the option count above which pickers enable huh's
// type-to-filter — below it, plain arrow-key nav is faster and avoids
// capturing stray keystrokes.
const filterThreshold = 8

// noInput, when true, disables all interactive prompts regardless of TTY
// state. Set by the CLI from --no-input / PMOX_NO_INPUT / --output json.
var noInput bool

// SetNoInput enables or disables interactive prompts process-wide.
func SetNoInput(v bool) { noInput = v }

// NoInput reports whether interactive prompts are disabled.
func NoInput() bool { return noInput }

// Interactive reports whether a picker/prompt may be drawn: input is not
// disabled and both stdin and stderr are terminals.
func Interactive() bool {
	return !noInput && StdinIsTerminal() && StderrIsTerminal()
}

// SelectOne runs a huh.Select with arrow-key navigation. Returns the
// chosen value, or the fallback on error. On user-abort (Ctrl+C / Esc) it
// re-raises SIGINT so the root signal handler cancels the process context.
// Kept for the configure/create-template wizards, where cancelling accepts
// the fallback default; standalone target pickers should use Select.
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
		Filtering(len(opts) > filterThreshold).
		WithTheme(Theme()).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		}
		return fallback
	}
	return selected
}

// Confirm runs a themed yes/no picker (huh.Confirm) and reports the choice.
// Use it in place of a raw "[y/N]" text prompt anywhere the wizard needs a
// binary decision. On abort (Ctrl+C) it re-raises SIGINT, like Select.
func Confirm(title string, defaultYes bool) (bool, error) {
	fmt.Println()
	answer := defaultYes
	err := huh.NewConfirm().
		Title(title).
		Affirmative("Yes").
		Negative("No").
		Value(&answer).
		WithTheme(Theme()).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		}
		return false, ErrCancelled
	}
	return answer, nil
}

// Select runs a single-choice picker and reports cancellation explicitly
// via ErrCancelled (rather than overloading a fallback value). Use it for
// standalone target selection (VMs, contexts) where an abort must not be
// mistaken for a real choice.
func Select(title string, opts []huh.Option[string]) (string, error) {
	if len(opts) == 0 {
		return "", ErrCancelled
	}
	if len(opts) == 1 {
		return opts[0].Value, nil
	}
	fmt.Println()
	selected := opts[0].Value
	err := huh.NewSelect[string]().
		Title(title).
		Options(opts...).
		Value(&selected).
		Filtering(len(opts) > filterThreshold).
		WithTheme(Theme()).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		}
		return "", ErrCancelled
	}
	return selected, nil
}

// SelectMulti runs a multi-choice picker (space toggles, enter confirms)
// and returns the chosen values. An empty selection or abort returns
// ErrCancelled so callers never proceed on "nothing selected".
// SelectMultiChecked runs a multi-select whose options may arrive
// pre-checked (via huh.NewOption(...).Selected(true)). Unlike SelectMulti,
// an empty final selection is returned as an empty slice (nil error), not
// ErrCancelled — deselecting everything is a valid "do nothing" choice.
// A user abort (Ctrl-C) still returns ErrCancelled.
func SelectMultiChecked(title string, opts []huh.Option[string]) ([]string, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	fmt.Println()
	var selected []string
	err := huh.NewMultiSelect[string]().
		Title(title).
		Options(opts...).
		Value(&selected).
		Filterable(len(opts) > filterThreshold).
		WithTheme(Theme()).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		}
		return nil, ErrCancelled
	}
	return selected, nil
}

func SelectMulti(title string, opts []huh.Option[string]) ([]string, error) {
	if len(opts) == 0 {
		return nil, ErrCancelled
	}
	fmt.Println()
	var selected []string
	err := huh.NewMultiSelect[string]().
		Title(title).
		Options(opts...).
		Value(&selected).
		Filterable(len(opts) > filterThreshold).
		WithTheme(Theme()).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		}
		return nil, ErrCancelled
	}
	if len(selected) == 0 {
		return nil, ErrCancelled
	}
	return selected, nil
}
