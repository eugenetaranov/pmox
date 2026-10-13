// Package run installs a plan: apt repositories and packages in one batch,
// then each item's other steps in order. An item fails at its first
// failing step; items that need it are skipped; the run goes on and
// reports every failure at the end.
package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/catalog"
	"github.com/eugenetaranov/pmox/internal/devbox/plan"
	"github.com/eugenetaranov/pmox/internal/devbox/steps"
)

// Progress shows one line per item; the terminal UI animates it.
type Progress interface {
	Start(title string)
	// Done ends the line: status is "ok", "done" (nothing to do), "fail"
	// or "skip"; detail is shown with it.
	Done(status, detail string)
}

// Plain is a Progress for logs and pipes.
type Plain struct{ W io.Writer }

func (p Plain) Start(string) {}

func (p Plain) Done(status, detail string) {
	mark := map[string]string{"ok": "✓", "done": "·", "fail": "✗", "skip": "-"}[status]
	fmt.Fprintf(p.W, "%s %s\n", mark, detail)
}

// Runner installs items against Env.
type Runner struct {
	Env      *steps.Env
	Progress Progress
	// TailLines of a failed item's output are shown with the failure.
	TailLines int
}

// Result is what happened to each item.
type Result struct {
	Changed, Unchanged, Failed, Skipped []string
	// Errors holds each failed item's error and output tail.
	Errors map[string]string
}

// OK reports whether nothing failed.
func (r *Result) OK() bool { return len(r.Failed) == 0 }

type built struct {
	item  *catalog.Item
	steps []steps.Step
	err   error
}

func build(items []*catalog.Item) []built {
	out := make([]built, len(items))
	for i, it := range items {
		out[i].item = it
		for _, s := range it.Steps {
			st, err := steps.New(it.ID, s)
			if err != nil {
				out[i].err = err
				break
			}
			out[i].steps = append(out[i].steps, st)
		}
	}
	return out
}

// Run installs items (in plan order) and then extra mise tools.
func (r *Runner) Run(ctx context.Context, items []*catalog.Item, extraMise []string) *Result {
	res := &Result{Errors: map[string]string{}}
	failed := map[string]bool{}
	fail := func(id string, err error, out []byte) {
		failed[id] = true
		res.Failed = append(res.Failed, id)
		msg := err.Error()
		if tail := tailLines(out, r.TailLines); tail != "" {
			msg += "\n" + tail
		}
		res.Errors[id] = msg
	}
	b := build(items)
	pkgDone := r.batchApt(ctx, b, failed, fail)

	for _, x := range b {
		it := x.item
		title := label(it)
		if failed[it.ID] {
			r.Progress.Done("fail", title+": "+firstLine(res.Errors[it.ID]))
			continue
		}
		if why := skipReason(it, failed); why != "" {
			failed[it.ID] = true
			res.Skipped = append(res.Skipped, it.ID)
			r.Progress.Done("skip", fmt.Sprintf("%s (skipped: %s failed)", title, why))
			continue
		}
		if x.err != nil {
			fail(it.ID, x.err, nil)
			r.Progress.Done("fail", title+": "+x.err.Error())
			continue
		}
		r.Progress.Start(title)
		var out bytes.Buffer
		changed, err := r.applyItem(ctx, x, pkgDone, &out)
		switch {
		case err != nil:
			fail(it.ID, err, out.Bytes())
			r.Progress.Done("fail", title)
		case changed:
			res.Changed = append(res.Changed, it.ID)
			r.Progress.Done("ok", title)
		default:
			res.Unchanged = append(res.Unchanged, it.ID)
			r.Progress.Done("done", title+" (already done)")
		}
	}

	if len(extraMise) > 0 {
		title := "mise: " + strings.Join(extraMise, " ")
		if failed[plan.MiseID] {
			res.Skipped = append(res.Skipped, "mise-extra")
			r.Progress.Done("skip", title+" (skipped: mise failed)")
		} else {
			r.Progress.Start(title)
			var out bytes.Buffer
			if done, _ := steps.MiseHas(ctx, r.Env, extraMise); done {
				res.Unchanged = append(res.Unchanged, "mise-extra")
				r.Progress.Done("done", title+" (already done)")
				return res
			}
			if err := r.withLog(&out, func() error { return steps.MiseUse(ctx, r.Env, extraMise) }); err != nil {
				fail("mise-extra", err, out.Bytes())
				r.Progress.Done("fail", title)
			} else {
				res.Changed = append(res.Changed, "mise-extra")
				r.Progress.Done("ok", title)
			}
		}
	}
	return res
}

// skipReason names the failed item it needs, or "".
func skipReason(it *catalog.Item, failed map[string]bool) string {
	for _, n := range it.Needs {
		if failed[n] {
			return n
		}
	}
	if it.UsesMise() && failed[plan.MiseID] {
		return plan.MiseID
	}
	return ""
}

// applyItem runs an item's steps; apt steps were batched (pkgDone).
func (r *Runner) applyItem(ctx context.Context, x built, pkgDone map[string]bool, out *bytes.Buffer) (changed bool, err error) {
	err = r.withLog(out, func() error {
		for _, st := range x.steps {
			if batched(st) {
				continue // apt repos and packages went in the batch
			}
			if f, ok := st.(steps.Follower); ok && f.Follows() {
				if !changed {
					continue
				}
			} else {
				done, err := st.Check(ctx, r.Env)
				if err != nil {
					return err
				}
				if done {
					continue
				}
			}
			if err := st.Apply(ctx, r.Env); err != nil {
				return err
			}
			changed = true
		}
		return nil
	})
	if pkgDone[x.item.ID+"\x00changed"] {
		changed = true
	}
	return changed, err
}

func batched(st steps.Step) bool {
	switch st.(type) {
	case interface{ Packages() []string }, interface{ Repo() bool }:
		return true
	}
	return false
}

// withLog sends the env's command log to the real log and to out.
func (r *Runner) withLog(out *bytes.Buffer, f func() error) error {
	old := r.Env.Log
	if old != nil {
		r.Env.Log = io.MultiWriter(old, out)
	} else {
		r.Env.Log = out
	}
	defer func() { r.Env.Log = old }()
	return f()
}

// batchApt adds every repository in the plan, refreshes the lists once,
// and installs every missing package in one transaction. When that fails,
// each item's packages are retried alone so the failure lands on its item.
// It returns "id\x00changed" for each item something was added for.
func (r *Runner) batchApt(ctx context.Context, b []built, failed map[string]bool, fail func(string, error, []byte)) map[string]bool {
	settled := map[string]bool{}
	var out bytes.Buffer
	reposChanged := false
	for _, x := range b {
		for _, st := range x.steps {
			if _, ok := st.(interface{ Repo() bool }); !ok {
				continue
			}
			done, _ := st.Check(ctx, r.Env)
			if done {
				continue
			}
			out.Reset()
			if err := r.withLog(&out, func() error { return st.Apply(ctx, r.Env) }); err != nil {
				fail(x.item.ID, err, out.Bytes())
				break
			}
			reposChanged = true
			settled[x.item.ID+"\x00changed"] = true
		}
	}

	need := map[string][]string{} // item → missing packages
	var all []string
	seen := map[string]bool{}
	for _, x := range b {
		if failed[x.item.ID] || skipReason(x.item, failed) != "" {
			continue
		}
		var pkgs []string
		for _, st := range x.steps {
			if p, ok := st.(interface{ Packages() []string }); ok {
				pkgs = append(pkgs, p.Packages()...)
			}
		}
		if len(pkgs) == 0 {
			continue
		}
		missing, _ := steps.MissingPackages(ctx, r.Env, pkgs)
		settled[x.item.ID] = true
		if len(missing) == 0 {
			continue
		}
		need[x.item.ID] = missing
		settled[x.item.ID+"\x00changed"] = true
		for _, p := range missing {
			if !seen[p] {
				seen[p] = true
				all = append(all, p)
			}
		}
	}
	if len(all) == 0 && !reposChanged {
		return settled
	}
	r.Progress.Start("apt: " + strings.Join(all, " "))
	out.Reset()
	err := r.withLog(&out, func() error {
		if err := steps.AptUpdate(ctx, r.Env); err != nil {
			return err
		}
		return steps.AptInstall(ctx, r.Env, all)
	})
	if err == nil {
		if len(all) > 0 {
			r.Progress.Done("ok", fmt.Sprintf("apt: %d packages", len(all)))
		} else {
			r.Progress.Done("ok", "apt: package lists")
		}
		return settled
	}
	msg := "apt: one transaction failed; retrying item by item"
	if tail := tailLines(out.Bytes(), 5); tail != "" {
		msg += "\n" + tail
	}
	r.Progress.Done("fail", msg)
	for _, x := range b {
		pkgs := need[x.item.ID]
		if len(pkgs) == 0 {
			continue
		}
		var o bytes.Buffer
		if err := r.withLog(&o, func() error { return steps.AptInstall(ctx, r.Env, pkgs) }); err != nil {
			fail(x.item.ID, err, o.Bytes())
		}
	}
	return settled
}

func label(it *catalog.Item) string {
	if it.Label != "" {
		return it.Label
	}
	return it.ID
}

func tailLines(b []byte, n int) string {
	if n <= 0 || len(b) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
