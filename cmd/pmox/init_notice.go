package main

// notice is one user-facing line produced by an init helper's pure core.
// The linear flow prints notices (warnings to stderr, info to stdout)
// exactly as the helpers used to print them inline; the interactive
// wizard renders them on the page they belong to. One notice is one
// line: multi-line messages are several notices, so styled stderr
// output stays line-for-line identical to the old inline Errf calls.
type notice struct {
	warn bool
	text string
}

func warnNotice(text string) notice { return notice{warn: true, text: text} }
func infoNotice(text string) notice { return notice{text: text} }

// printNotices writes notices through p, one line each.
func printNotices(p prompter, ns []notice) {
	for _, n := range ns {
		if n.warn {
			p.Errf("%s\n", n.text)
		} else {
			p.Printf("%s\n", n.text)
		}
	}
}
