// Package tui holds small shared terminal UI helpers used across pmox
// commands. Everything here is a thin wrapper around charmbracelet/huh,
// deliberately kept narrow so callers don't reach for huh directly.
package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
)

// ErrCancelled is returned by pickers when no usable choice was made
// (nothing to pick from, an empty multi-selection, or a non-abort
// terminal error).
var ErrCancelled = errors.New("selection cancelled")

// ErrAborted is returned by every picker and confirm prompt when the user
// aborts it (Ctrl-C). Callers must propagate it rather than proceed with a
// default; the CLI maps it to exit code 130 ("Interrupted.").
var ErrAborted = errors.New("interrupted")

// AbortErr maps huh's user-abort error to ErrAborted and returns any other
// error unchanged. Use it when running a huh field or form directly.
func AbortErr(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

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

// SelectOne runs a huh.Select with arrow-key navigation and returns the
// chosen value. A user abort returns ErrAborted — never the fallback. Any
// other picker failure (or an empty option list) yields the fallback.
// Kept for the init/create-template wizards, where a broken terminal
// accepts the default; standalone target pickers should use Select.
func SelectOne(title string, opts []huh.Option[string], fallback string) (string, error) {
	if len(opts) == 0 {
		return fallback, nil
	}
	if len(opts) == 1 {
		return opts[0].Value, nil
	}
	selected, err := runPicker(opts[0].Value, func(v *string) huh.Field {
		return selectField(title, opts, v)
	})
	if errors.Is(err, ErrAborted) {
		return "", err
	}
	if err != nil {
		return fallback, nil
	}
	return selected, nil
}

// Confirm runs a themed yes/no picker (huh.Confirm) and reports the choice.
// Use it in place of a raw "[y/N]" text prompt anywhere the wizard needs a
// binary decision. On abort (Ctrl+C) it returns ErrAborted, like Select.
func Confirm(title string, defaultYes bool) (bool, error) {
	return runPicker(defaultYes, func(v *bool) huh.Field {
		return huh.NewConfirm().
			Title(title).
			Affirmative("Yes").
			Negative("No").
			Value(v).
			WithTheme(Theme())
	})
}

// Select runs a single-choice picker and reports cancellation explicitly
// via ErrAborted/ErrCancelled (rather than overloading a fallback value). Use it for
// standalone target selection (VMs, contexts) where an abort must not be
// mistaken for a real choice.
func Select(title string, opts []huh.Option[string]) (string, error) {
	if len(opts) == 0 {
		return "", ErrCancelled
	}
	if len(opts) == 1 {
		return opts[0].Value, nil
	}
	return runPicker(opts[0].Value, func(v *string) huh.Field {
		return selectField(title, opts, v)
	})
}

// SelectMulti runs a multi-choice picker (space toggles, enter confirms)
// and returns the chosen values. An empty selection returns ErrCancelled
// and an abort ErrAborted, so callers never proceed on "nothing selected".
func SelectMulti(title string, opts []huh.Option[string]) ([]string, error) {
	if len(opts) == 0 {
		return nil, ErrCancelled
	}
	selected, err := runPicker(nil, func(v *[]string) huh.Field {
		return multiSelectField(title, opts, v)
	})
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, ErrCancelled
	}
	return selected, nil
}

// SelectMultiChecked runs a multi-select whose options may arrive
// pre-checked (via huh.NewOption(...).Selected(true)). Unlike SelectMulti,
// an empty final selection is returned as an empty slice (nil error), not
// ErrCancelled — deselecting everything is a valid "do nothing" choice.
// A user abort (Ctrl-C) still returns ErrAborted.
func SelectMultiChecked(title string, opts []huh.Option[string]) ([]string, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	return runPicker(nil, func(v *[]string) huh.Field {
		return multiSelectField(title, opts, v)
	})
}

// runPicker prints a blank separator line, runs the field built around a
// value seeded with initial, and returns the final value. A user abort
// (Ctrl+C) is reported as ErrAborted; any other error as ErrCancelled.
func runPicker[T any](initial T, build func(*T) huh.Field) (T, error) {
	fmt.Println()
	value := initial
	if err := build(&value).Run(); err != nil {
		var zero T
		if errors.Is(err, huh.ErrUserAborted) {
			return zero, ErrAborted
		}
		return zero, ErrCancelled
	}
	return value, nil
}

func selectField(title string, opts []huh.Option[string], v *string) huh.Field {
	return huh.NewSelect[string]().
		Title(title).
		Options(opts...).
		Value(v).
		Filtering(len(opts) > filterThreshold).
		WithTheme(Theme())
}

func multiSelectField(title string, opts []huh.Option[string], v *[]string) huh.Field {
	return huh.NewMultiSelect[string]().
		Title(title).
		Options(opts...).
		Value(v).
		Filterable(len(opts) > filterThreshold).
		WithTheme(Theme())
}
