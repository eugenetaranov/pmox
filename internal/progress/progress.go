// Package progress defines the phase-level UI callback interface shared
// by the launch and create-template state machines.
package progress

// Reporter receives phase-level UI callbacks. A nil Reporter is valid —
// Start and Done below no-op on it. Start is called before a phase
// begins; Done is called after the phase completes (err is nil on
// success). Implementations must be safe to call from a single
// goroutine in order.
type Reporter interface {
	Start(step string)
	Done(err error)
}

// Start calls p.Start(step) when p is non-nil.
func Start(p Reporter, step string) {
	if p != nil {
		p.Start(step)
	}
}

// Done calls p.Done(err) when p is non-nil.
func Done(p Reporter, err error) {
	if p != nil {
		p.Done(err)
	}
}
