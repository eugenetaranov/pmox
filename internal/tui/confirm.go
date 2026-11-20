package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type Confirmer interface {
	Confirm(ctx context.Context, prompt string) (bool, error)
}

type ttyConfirmer struct {
	in  io.Reader
	out io.Writer
}

func NewTTYConfirmer(in io.Reader, out io.Writer) Confirmer {
	return &ttyConfirmer{in: in, out: out}
}

func (c *ttyConfirmer) Confirm(_ context.Context, prompt string) (bool, error) {
	_, _ = io.WriteString(c.out, prompt)
	scanner := bufio.NewScanner(c.in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

// confirmIn and confirmOut are ConfirmLine's terminal; tests replace them.
var (
	confirmIn  io.Reader = os.Stdin
	confirmOut io.Writer = os.Stderr
)

// ConfirmLine asks a yes/no question as one typed line, "title [y/N]: "
// (or "[Y/n]" when the default is yes). It is the confirmation every
// command uses; only the full-screen wizards use the button-style
// Confirm. An empty answer takes the default; end of input (Ctrl-D)
// returns ErrAborted.
func ConfirmLine(title string, defaultYes bool) (bool, error) {
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	_, _ = fmt.Fprintf(confirmOut, "%s %s: ", title, hint)
	scanner := bufio.NewScanner(confirmIn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		_, _ = fmt.Fprintln(confirmOut)
		return false, ErrAborted
	}
	switch strings.TrimSpace(strings.ToLower(scanner.Text())) {
	case "":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

type AlwaysConfirmer struct{}

func (AlwaysConfirmer) Confirm(context.Context, string) (bool, error) {
	return true, nil
}

var StdinIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

var StderrIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}
