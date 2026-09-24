package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/muesli/cancelreader"
	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// prompter abstracts terminal I/O so tests can drive configure with a fake.
type prompter interface {
	Prompt(msg string) (string, error)
	PromptSecret(msg string) (string, error)
	Printf(format string, args ...interface{})
	Errf(format string, args ...interface{})
	// In and Out expose the raw terminal streams for flows that drive
	// their own dialogue (host-key pinning). In must share any buffering
	// Prompt uses so no typed-ahead input is lost.
	In() io.Reader
	Out() io.Writer
}

type stdPrompter struct {
	in     *bufio.Reader
	cancel cancelreader.CancelReader
	out    io.Writer
	err    io.Writer
}

func newStdPrompter(ctx context.Context) *stdPrompter {
	cr, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		// Fall back to uncancellable bufio on platforms cancelreader can't handle.
		return &stdPrompter{
			in:  bufio.NewReader(os.Stdin),
			out: os.Stdout,
			err: os.Stderr,
		}
	}
	p := &stdPrompter{
		in:     bufio.NewReader(cr),
		cancel: cr,
		out:    os.Stdout,
		err:    os.Stderr,
	}
	// Cancel the underlying read as soon as the context is done (e.g. on SIGINT).
	go func() {
		<-ctx.Done()
		cr.Cancel()
	}()
	return p
}

func (p *stdPrompter) Prompt(msg string) (string, error) {
	fmt.Fprint(p.out, msg)
	line, err := p.in.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil {
		// cancelreader.ErrCanceled → user hit Ctrl+C; io.EOF with no data same.
		if errors.Is(err, cancelreader.ErrCanceled) || (errors.Is(err, io.EOF) && line == "") {
			return "", fmt.Errorf("%w: interrupted", exitcode.ErrUserInput)
		}
		if errors.Is(err, io.EOF) {
			return line, nil
		}
		return "", err
	}
	return line, nil
}

func (p *stdPrompter) PromptSecret(msg string) (string, error) {
	fmt.Fprint(p.out, msg)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(p.out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (p *stdPrompter) In() io.Reader  { return p.in }
func (p *stdPrompter) Out() io.Writer { return p.out }

func (p *stdPrompter) Printf(format string, args ...interface{}) {
	fmt.Fprintf(p.out, format, args...)
}

func (p *stdPrompter) Errf(format string, args ...interface{}) {
	fmt.Fprint(p.err, tui.Warnf(fmt.Sprintf(format, args...)))
}
