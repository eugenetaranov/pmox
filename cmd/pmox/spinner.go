package main

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/template"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// Progress feedback (openspec/specs/cli-progress-feedback): every wait on
// the cluster shows a delayed spinner on a terminal, and ends in a "✓"
// result line or is cleared before an error or a prompt.

// startSpin starts a spinner labelled label on stderr; nil (a valid,
// no-op spinner) when stderr isn't a terminal or spinners are off.
func startSpin(label string) *tui.Spinner { return tui.StartSpinner(label) }

// finishSpin stops sp and prints msg as the command's result on stdout,
// with a "✓" when a spinner was running.
func finishSpin(cmd *cobra.Command, sp *tui.Spinner, msg string) {
	if sp == nil {
		fmt.Fprintln(cmd.OutOrStdout(), msg)
		return
	}
	sp.Stop()
	fmt.Fprintf(cmd.OutOrStdout(), "✓ %s\n", msg)
}

// doneMark is the "✓ " prefix for a result line when progress was shown.
func doneMark(shown bool) string {
	if shown {
		return "✓ "
	}
	return ""
}

// stepSpinner is the launch.Progress / template.Progress implementation:
// one spinner per step, leaving "✓ <step>" when the step succeeds and a
// cleared line when it fails (the caller reports the error). Sequential
// Start/Done calls only.
type stepSpinner struct {
	w    io.Writer
	mu   sync.Mutex
	sp   *tui.Spinner
	step string
}

func newStepSpinner(w io.Writer) *stepSpinner { return &stepSpinner{w: w} }

// Start begins the spinner for step. A previous step must be ended with
// Done first.
func (s *stepSpinner) Start(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.step, s.sp = step, tui.NewSpinner(s.w, step)
}

// Done ends the current step: "✓ <step>" on success, a cleared line on
// error.
func (s *stepSpinner) Done(err error) {
	s.mu.Lock()
	sp, step := s.sp, s.step
	s.sp = nil
	s.mu.Unlock()
	if sp == nil {
		return
	}
	if err != nil {
		sp.Stop()
		return
	}
	sp.Succeed(step)
}

// newLaunchProgress returns a launch.Progress for this invocation: a
// spinner when stderr is a terminal and spinners are on, else nil.
func newLaunchProgress(stderr io.Writer) launch.Progress {
	if s := newTTYSpinner(stderr); s != nil {
		return s
	}
	return nil
}

// newTemplateProgress is newLaunchProgress for template builds.
func newTemplateProgress(stderr io.Writer) template.Progress {
	if s := newTTYSpinner(stderr); s != nil {
		return s
	}
	return nil
}

// newTTYSpinner returns a stepSpinner when stderr is a terminal and
// verbose is off; otherwise nil.
func newTTYSpinner(stderr io.Writer) *stepSpinner {
	if verbose || debug {
		return nil
	}
	f, ok := stderr.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	return newStepSpinner(stderr)
}
