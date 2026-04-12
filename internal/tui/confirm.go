package tui

import (
	"bufio"
	"context"
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

type AlwaysConfirmer struct{}

func (AlwaysConfirmer) Confirm(context.Context, string) (bool, error) {
	return true, nil
}

var StdinIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
