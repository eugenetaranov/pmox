package tui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// SpinnerDelay is how long an operation runs before its spinner is drawn.
// Anything quicker never shows one, so fast calls don't flash a line that
// vanishes at once.
var SpinnerDelay = 250 * time.Millisecond

var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

var spinnersOff bool

// active counts running spinners; StartSpinner draws only one at a time.
var (
	activeMu sync.Mutex
	active   int
)

// SetSpinners turns progress spinners on or off for this process (off for
// verbose output, which would interleave with the redrawn line).
func SetSpinners(on bool) { spinnersOff = !on }

// Spinner is a one-line progress indicator for a slow operation. A nil
// *Spinner is valid and does nothing, so callers needn't check whether
// one was started.
type Spinner struct {
	w      io.Writer
	mu     sync.Mutex
	label  string
	drawn  bool
	stop   chan struct{}
	doneCh chan struct{}
}

// StartSpinner starts a spinner on stderr labelled label, or returns nil
// when stderr isn't a terminal, spinners are off, or another spinner is
// already running (the outer operation's spinner covers nested steps).
func StartSpinner(label string) *Spinner {
	if spinnersOff || !StderrIsTerminal() {
		return nil
	}
	activeMu.Lock()
	busy := active > 0
	activeMu.Unlock()
	if busy {
		return nil
	}
	return NewSpinner(os.Stderr, label)
}

// NewSpinner starts a spinner on w regardless of whether it's a terminal.
func NewSpinner(w io.Writer, label string) *Spinner {
	s := &Spinner{w: w, label: label, stop: make(chan struct{}), doneCh: make(chan struct{})}
	activeMu.Lock()
	active++
	activeMu.Unlock()
	go s.run(s.stop)
	return s
}

func (s *Spinner) run(stop <-chan struct{}) {
	defer close(s.doneCh)
	select {
	case <-stop:
		return
	case <-time.After(SpinnerDelay):
	}
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		s.mu.Lock()
		fmt.Fprintf(s.w, "\r\033[K%c %s", spinnerFrames[i%len(spinnerFrames)], s.label)
		s.drawn = true
		s.mu.Unlock()
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// Set changes the label in place.
func (s *Spinner) Set(label string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.label = label
	s.mu.Unlock()
}

// Stop ends the spinner and clears its line. Safe to call more than once.
func (s *Spinner) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	stop := s.stop
	s.stop = nil
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-s.doneCh
	activeMu.Lock()
	active--
	activeMu.Unlock()
	if s.drawn {
		fmt.Fprint(s.w, "\r\033[K")
	}
}

// Succeed ends the spinner and leaves "✓ msg" in its place.
func (s *Spinner) Succeed(msg string) {
	if s == nil {
		return
	}
	s.Stop()
	fmt.Fprintf(s.w, "✓ %s\n", msg)
}

// Writer returns w wrapped so that each write first clears the spinner's
// line; the next frame redraws the spinner below the written text. Use it
// for notes printed while a spinner runs. On a nil spinner it returns w.
func (s *Spinner) Writer(w io.Writer) io.Writer {
	if s == nil {
		return w
	}
	return spinnerWriter{s: s, w: w}
}

type spinnerWriter struct {
	s *Spinner
	w io.Writer
}

func (sw spinnerWriter) Write(p []byte) (int, error) {
	sw.s.mu.Lock()
	defer sw.s.mu.Unlock()
	if sw.s.drawn {
		fmt.Fprint(sw.s.w, "\r\033[K")
	}
	return sw.w.Write(p)
}
