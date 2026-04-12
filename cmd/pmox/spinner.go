package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/eugenetaranov/pmox/internal/launch"
	"github.com/eugenetaranov/pmox/internal/template"
)

// stepSpinner renders a single-line braille spinner for launch phases
// on a TTY. On Done(nil) it clears the line and prints "✓ <step>".
// On Done(err) it clears the line and lets the caller surface the
// error. Safe for sequential Start/Done calls.
type stepSpinner struct {
	w      io.Writer
	mu     sync.Mutex
	frames []rune
	step   string
	stop   chan struct{}
	doneCh chan struct{}
}

func newStepSpinner(w io.Writer) *stepSpinner {
	return &stepSpinner{
		w:      w,
		frames: []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'},
	}
}

// Start begins a new spinner frame loop for the given step label.
// A previously-started step must be ended with Done before calling Start again.
func (s *stepSpinner) Start(step string) {
	s.mu.Lock()
	s.step = step
	s.stop = make(chan struct{})
	s.doneCh = make(chan struct{})
	stopCh := s.stop
	doneCh := s.doneCh
	s.mu.Unlock()

	go func() {
		defer close(doneCh)
		i := 0
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for {
			s.mu.Lock()
			fmt.Fprintf(s.w, "\r\033[K%c %s", s.frames[i%len(s.frames)], s.step)
			s.mu.Unlock()
			select {
			case <-stopCh:
				return
			case <-t.C:
				i++
			}
		}
	}()
}

// Done stops the active spinner. On success it prints "✓ <step>";
// on error it clears the line so the caller's error message (usually
// printed by main) stands alone.
func (s *stepSpinner) Done(err error) {
	s.mu.Lock()
	stopCh := s.stop
	doneCh := s.doneCh
	step := s.step
	s.stop = nil
	s.doneCh = nil
	s.mu.Unlock()

	if stopCh == nil {
		return
	}
	close(stopCh)
	<-doneCh

	if err != nil {
		fmt.Fprint(s.w, "\r\033[K")
		return
	}
	fmt.Fprintf(s.w, "\r\033[K✓ %s\n", step)
}

// newLaunchProgress returns a launch.Progress suitable for the current
// invocation. It returns a TTY spinner when stderr is a terminal and
// -v is NOT set (verbose output would interleave badly with the
// redrawn spinner line). Otherwise it returns a no-op.
func newLaunchProgress(stderr io.Writer) launch.Progress {
	if s := newTTYSpinner(stderr); s != nil {
		return s
	}
	return nil
}

// newTemplateProgress returns a template.Progress suitable for the
// current invocation, following the same TTY/verbose rules as
// newLaunchProgress.
func newTemplateProgress(stderr io.Writer) template.Progress {
	if s := newTTYSpinner(stderr); s != nil {
		return s
	}
	return nil
}

// newTTYSpinner returns a stepSpinner when stderr is a TTY and verbose
// is off; otherwise nil. Shared by launch and create-template.
func newTTYSpinner(stderr io.Writer) *stepSpinner {
	if verbose {
		return nil
	}
	f, ok := stderr.(*os.File)
	if !ok {
		return nil
	}
	if !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	return newStepSpinner(stderr)
}
