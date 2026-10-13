package run

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/eugenetaranov/pmox/internal/tui"
)

// Live is a Progress for a terminal: a spinner with what is happening and
// for how long, replaced by a result line when the step ends.
type Live struct {
	W io.Writer
	// Width is the terminal's width, for cutting long labels (0: 80).
	Width int

	mu    sync.Mutex
	sp    *tui.Spinner
	stop  chan struct{}
	title string
}

func (l *Live) Start(title string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.end()
	l.title = title
	start := time.Now()
	l.sp = tui.NewSpinner(l.W, l.label(title, 0))
	l.stop = make(chan struct{})
	go func(sp *tui.Spinner, stop <-chan struct{}) {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				sp.Set(l.label(title, time.Since(start)))
			}
		}
	}(l.sp, l.stop)
}

func (l *Live) Done(status, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.end()
	Plain{W: l.W}.Done(status, detail)
}

// Pause stops the spinner without a result line, for a step that needs
// the terminal (an interactive login); the step's Done still prints.
func (l *Live) Pause() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.end()
}

// end stops the running spinner, if any (l.mu held).
func (l *Live) end() {
	if l.sp == nil {
		return
	}
	close(l.stop)
	l.sp.Stop()
	l.sp, l.stop = nil, nil
}

// label is the spinner's text: the step, cut to fit, and the time so far.
func (l *Live) label(title string, d time.Duration) string {
	w := l.Width
	if w <= 0 {
		w = 80
	}
	suffix := ""
	if d >= time.Second {
		suffix = " (" + d.Truncate(time.Second).String() + ")"
	}
	// The spinner's frame and a space come first.
	room := w - 3 - len([]rune(suffix))
	r := []rune(title)
	if room > 1 && len(r) > room {
		title = string(r[:room-1]) + "…"
	}
	return fmt.Sprintf("%s%s", title, suffix)
}
