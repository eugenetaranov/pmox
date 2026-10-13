package ui

import (
	"errors"

	"github.com/charmbracelet/huh"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// ErrQuit is returned when the user quits a prompt (esc / ctrl-c).
var ErrQuit = errors.New("cancelled")

func run(f huh.Field) error {
	err := huh.NewForm(huh.NewGroup(f)).WithTheme(tui.Theme()).WithShowHelp(false).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrQuit
	}
	return err
}

// Input asks for a line of text, prefilled with value.
func Input(title, desc, value string) (string, error) {
	v := value
	err := run(huh.NewInput().Title(title).Description(desc).Value(&v))
	return v, err
}

// Password asks for a secret, without echo; empty keeps nothing.
func Password(title, placeholder string) (string, error) {
	var v string
	err := run(huh.NewInput().Title(title).Placeholder(placeholder).EchoMode(huh.EchoModePassword).Value(&v))
	return v, err
}

// Choose asks for one of opts, starting on selected.
func Choose(title string, opts []string, selected string) (string, error) {
	v := selected
	o := make([]huh.Option[string], len(opts))
	for i, s := range opts {
		o[i] = huh.NewOption(s, s)
	}
	err := run(huh.NewSelect[string]().Title(title).Options(o...).Value(&v))
	return v, err
}

// Confirm asks yes or no.
func Confirm(title, desc string, def bool) (bool, error) {
	v := def
	err := run(huh.NewConfirm().Title(title).Description(desc).Value(&v))
	return v, err
}
