package doctor

import (
	"testing"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/pveclient"
)

func TestFinalize_ReadyWhenAllPass(t *testing.T) {
	cl := &Checklist{}
	cl.Pass("a", "config", "ok")
	cl.Pass("b", "api", "ok")
	r := cl.Finalize("https://pve", "test", false)
	if !r.Ready || r.Verdict != "ready" || r.ExitCode != exitcode.ExitOK {
		t.Fatalf("ready=%v verdict=%s exit=%d", r.Ready, r.Verdict, r.ExitCode)
	}
}

func TestFinalize_WarningsDoNotBlockUnlessStrict(t *testing.T) {
	build := func() *Checklist {
		cl := &Checklist{}
		cl.Pass("a", "config", "ok")
		cl.Warn("b", "config", "meh", "fix it")
		return cl
	}
	lenient := build().Finalize("s", "src", false)
	if !lenient.Ready || lenient.ExitCode != exitcode.ExitOK {
		t.Errorf("lenient: ready=%v exit=%d, want ready & 0", lenient.Ready, lenient.ExitCode)
	}
	strict := build().Finalize("s", "src", true)
	if strict.Ready || strict.ExitCode != exitcode.ExitWarnings {
		t.Errorf("strict: ready=%v exit=%d, want not-ready & ExitWarnings", strict.Ready, strict.ExitCode)
	}
}

func TestFinalize_ExitCodeIsWorstFailure(t *testing.T) {
	cl := &Checklist{}
	cl.Fail("net", "api", "unreachable", "", exitcode.ExitNetworkError)
	cl.Fail("auth", "api", "401", "", exitcode.ExitUnauthorized) // higher severity
	cl.Fail("nf", "template", "missing", "", exitcode.ExitNotFound)
	r := cl.Finalize("s", "src", false)
	if r.ExitCode != exitcode.ExitUnauthorized {
		t.Fatalf("exit=%d, want ExitUnauthorized (worst severity)", r.ExitCode)
	}
	if r.Summary.Fail != 3 {
		t.Errorf("fail count=%d, want 3", r.Summary.Fail)
	}
}

func TestFinalize_FailWithZeroExitFallsBackToGeneric(t *testing.T) {
	cl := &Checklist{}
	cl.Fail("x", "config", "bad", "", 0)
	r := cl.Finalize("s", "src", false)
	if r.ExitCode != exitcode.ExitGeneric {
		t.Fatalf("exit=%d, want ExitGeneric", r.ExitCode)
	}
}

func TestMissingPrivileges(t *testing.T) {
	perms := pveclient.Permissions{
		"/":     {"Sys.Audit": true},
		"/vms":  {"VM.Audit": true, "VM.Allocate": true},
		"/storage/local": {"Datastore.AllocateSpace": true},
	}
	required := []RequiredPriv{
		{"Sys.Audit", "/", ""},                              // present at /
		{"VM.Audit", "/vms", ""},                            // present at /vms
		{"VM.Clone", "/vms", ""},                            // MISSING
		{"Datastore.AllocateSpace", "/storage/local", ""},   // present
		{"Datastore.Audit", "/storage", ""},                 // MISSING
	}
	missing := MissingPrivileges(perms, required)
	if len(missing) != 2 {
		t.Fatalf("missing=%d, want 2: %+v", len(missing), missing)
	}
	names := map[string]bool{}
	for _, m := range missing {
		names[m.Priv] = true
	}
	if !names["VM.Clone"] || !names["Datastore.Audit"] {
		t.Errorf("missing set = %+v, want VM.Clone and Datastore.Audit", missing)
	}
}

func TestMissingPrivileges_InheritsFromRoot(t *testing.T) {
	// A role granted on "/" with propagation covers all descendant paths.
	perms := pveclient.Permissions{
		"/": {"Sys.Audit": true, "VM.Audit": true, "VM.Clone": true, "Datastore.Audit": true, "Datastore.AllocateSpace": true},
	}
	required := RequiredPrivileges("local", "local")
	missing := MissingPrivileges(perms, required)
	for _, m := range missing {
		// Everything required should be considered granted via "/".
		if perms.HasPriv(m.Path, m.Priv) {
			t.Errorf("%s on %s reported missing but is granted at /", m.Priv, m.Path)
		}
	}
}
