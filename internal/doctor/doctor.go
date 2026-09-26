// Package doctor models the results of `pmox doctor` — a read-only set
// of checks that validate configuration, Proxmox connectivity, and
// readiness. It holds the check/report types, the verdict and
// exit-code logic, and text/JSON rendering. The actual probing lives in
// the CLI layer, which appends results via Checklist.
package doctor

import (
	"context"

	"github.com/eugenetaranov/pmox/internal/exitcode"
)

// SchemaVersion is the version of the --json output contract. Bump it on
// any breaking change to the JSON shape or check-ID semantics.
const SchemaVersion = 1

// Status is a single check's outcome.
type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
	Info Status = "info"
)

// Fix describes an action `pmox doctor --fix` can offer to run to
// resolve a check. Attached via Checklist.WithFix, immediately after
// the check it repairs.
type Fix struct {
	// Prompt is shown for the "do this now? [y/N]" confirmation.
	Prompt string
	// Run performs the fix. A returned error is reported but never
	// stops the rest of the fix pass.
	Run func(ctx context.Context) error
	// RequiresTTY marks a fix that itself needs an interactive terminal
	// — e.g. it shells into its own picker flow (like
	// 'pmox create-template's image/storage prompts). Such a fix must
	// never run via -y/PMOX_ASSUME_YES alone without a real TTY,
	// regardless of how the outer "run this? [y/N]" was satisfied, or
	// it would hang or misbehave non-interactively.
	RequiresTTY bool
}

// Check is one diagnostic result. ID is a stable, scriptable identifier
// (treat as a public contract); Group buckets related checks.
type Check struct {
	ID          string `json:"id"`
	Group       string `json:"group"`
	Status      Status `json:"status"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`

	// exit is the process exit code this check contributes when it fails.
	// Not serialized — the report's top-level ExitCode is what callers use.
	exit int

	// fix, when set, is the interactive remediation --fix can offer for
	// this check. Not serialized — --fix is unavailable with
	// --output json anyway (there is no terminal to confirm on).
	fix *Fix
}

// Fixable reports whether this check has an attached Fix.
func (c Check) Fixable() bool { return c.fix != nil }

// FixPrompt returns the attached Fix's confirmation prompt, or "" if
// this check has no fix.
func (c Check) FixPrompt() string {
	if c.fix == nil {
		return ""
	}
	return c.fix.Prompt
}

// RunFix executes the attached Fix. A no-op returning nil if this check
// has no fix.
func (c Check) RunFix(ctx context.Context) error {
	if c.fix == nil {
		return nil
	}
	return c.fix.Run(ctx)
}

// FixRequiresTTY reports whether this check's fix must not be run
// without a real interactive terminal, even when otherwise approved
// (e.g. via -y). False (safe to auto-apply) when there is no fix at all.
func (c Check) FixRequiresTTY() bool {
	return c.fix != nil && c.fix.RequiresTTY
}

// Summary counts checks by outcome.
type Summary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Info int `json:"info"`
}

// Report is the full doctor result, including the computed verdict and
// exit code. It is the --json payload.
type Report struct {
	SchemaVersion int     `json:"schema_version"`
	Server        string  `json:"server,omitempty"`
	ServerSource  string  `json:"server_source,omitempty"`
	Verdict       string  `json:"verdict"` // "ready" | "not_ready"
	Ready         bool    `json:"ready"`
	ExitCode      int     `json:"exit_code"`
	Strict        bool    `json:"strict"`
	Checks        []Check `json:"checks"`
	Summary       Summary `json:"summary"`
}

// Checklist accumulates checks in order as the CLI probes each thing.
type Checklist struct {
	checks []Check
}

// Pass records a satisfied check.
func (l *Checklist) Pass(id, group, msg string) {
	l.checks = append(l.checks, Check{ID: id, Group: group, Status: Pass, Message: msg})
}

// Info records a non-actionable informational note (never affects the
// verdict) — e.g. "TLS verification disabled (configured)".
func (l *Checklist) Info(id, group, msg string) {
	l.checks = append(l.checks, Check{ID: id, Group: group, Status: Info, Message: msg})
}

// Warn records a non-blocking issue with a remediation hint. Warnings do
// not block readiness unless --strict is set.
func (l *Checklist) Warn(id, group, msg, remediation string) {
	l.checks = append(l.checks, Check{ID: id, Group: group, Status: Warn, Message: msg, Remediation: remediation})
}

// Fail records a blocking issue. exit is the process exit code this
// failure maps to (from internal/exitcode); the report picks the
// highest-severity code across all failures.
func (l *Checklist) Fail(id, group, msg, remediation string, exit int) {
	l.checks = append(l.checks, Check{ID: id, Group: group, Status: Fail, Message: msg, Remediation: remediation, exit: exit})
}

// WithFix attaches fix to the most recently recorded check, so
// `pmox doctor --fix` can offer to run it. Call immediately after the
// Warn/Fail it repairs. A no-op if nothing has been recorded yet, or
// the last check was a Pass/Info — a fix only makes sense for a
// check that found something wrong.
func (l *Checklist) WithFix(fix Fix) {
	if len(l.checks) == 0 {
		return
	}
	last := &l.checks[len(l.checks)-1]
	if last.Status != Warn && last.Status != Fail {
		return
	}
	last.fix = &fix
}

// Has reports whether a check with the given id has already been
// recorded — lets the CLI skip dependent checks after a prerequisite
// failed without threading booleans everywhere.
func (l *Checklist) Has(id string) bool {
	for _, c := range l.checks {
		if c.ID == id {
			return true
		}
	}
	return false
}

// StatusOf returns the status recorded for id, or "" if absent.
func (l *Checklist) StatusOf(id string) Status {
	for _, c := range l.checks {
		if c.ID == id {
			return c.Status
		}
	}
	return ""
}

// severityRank orders exit codes by how much a caller cares, highest
// first. Used to pick a deterministic exit code when several checks fail
// with different categories.
func severityRank(code int) int {
	switch code {
	case exitcode.ExitUnauthorized:
		return 7
	case exitcode.ExitNetworkError:
		return 6
	case exitcode.ExitTimeout:
		return 5
	case exitcode.ExitAPIError:
		return 4
	case exitcode.ExitNotFound:
		return 3
	case exitcode.ExitUserError:
		return 2
	default:
		return 1 // ExitGeneric and anything unmapped
	}
}

// Finalize computes the summary, verdict, and exit code and returns the
// report. strict promotes warnings to blocking (exit ExitWarnings when
// warnings are the only problem).
func (l *Checklist) Finalize(server, source string, strict bool) Report {
	r := Report{
		SchemaVersion: SchemaVersion,
		Server:        server,
		ServerSource:  source,
		Strict:        strict,
		Checks:        l.checks,
	}
	worstFail := 0
	for _, c := range l.checks {
		switch c.Status {
		case Pass:
			r.Summary.Pass++
		case Warn:
			r.Summary.Warn++
		case Fail:
			r.Summary.Fail++
			if worstFail == 0 || severityRank(c.exit) > severityRank(worstFail) {
				worstFail = c.exit
			}
		case Info:
			r.Summary.Info++
		}
	}

	switch {
	case r.Summary.Fail > 0:
		r.Ready = false
		r.ExitCode = worstFail
		if r.ExitCode == 0 {
			r.ExitCode = exitcode.ExitGeneric
		}
	case strict && r.Summary.Warn > 0:
		r.Ready = false
		r.ExitCode = exitcode.ExitWarnings
	default:
		r.Ready = true
		r.ExitCode = exitcode.ExitOK
	}
	if r.Ready {
		r.Verdict = "ready"
	} else {
		r.Verdict = "not_ready"
	}
	return r
}
