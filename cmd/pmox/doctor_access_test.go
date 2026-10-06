package main

import (
	"context"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/accessreg"
	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/doctor"
	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/server"
)

func runDoctorAccess(t *testing.T, perms pveclient.Permissions) map[string]doctor.Check {
	t.Helper()
	orig := doctorAccessPermsFn
	doctorAccessPermsFn = func(context.Context, *pveclient.Client) (pveclient.Permissions, error) { return perms, nil }
	t.Cleanup(func() { doctorAccessPermsFn = orig })
	cfg, _ := config.Load()
	r, err := server.Resolve(context.Background(), server.Options{Cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	cl := &doctor.Checklist{}
	doctorAccess(context.Background(), cl, nil, r, cfg)
	out := map[string]doctor.Check{}
	for _, c := range cl.Finalize("", "", false).Checks {
		out[c.ID] = c
	}
	return out
}

func TestDoctorAccess(t *testing.T) {
	ctx := context.Background()
	reg, guests := setupAccessEnv(t)
	withNodeSSH(t)

	got := runDoctorAccess(t, pveclient.Permissions{"/": {"Sys.Audit": true}})
	if c := got["access.privileges"]; c.Status != doctor.Info || !strings.Contains(c.Message, "VM.GuestAgent.FileWrite") {
		t.Errorf("privileges = %+v", c)
	}
	if c := got["access.registry"]; c.Status != doctor.Info || !strings.Contains(c.Message, "not used yet") {
		t.Errorf("empty registry = %+v", c)
	}

	publishAs(ctx, t, reg, "bob", testKeyA)
	_, _ = accessreg.UpdateAccess(ctx, reg, func(a *accessreg.Access) error { a.GrantVMs("bob", 101); return nil })
	got = runDoctorAccess(t, pveclient.Permissions{"/vms": {"VM.GuestAgent.FileRead": true, "VM.GuestAgent.FileWrite": true}})
	if c := got["access.privileges"]; c.Status != doctor.Pass {
		t.Errorf("privileges = %+v", c)
	}
	if c := got["access.drift"]; c.Status != doctor.Warn || !strings.Contains(c.Message, "web1") || !strings.Contains(c.Remediation, "pmox access sync") {
		t.Errorf("drift = %+v", c)
	}

	if _, err := runCmd(t, "access", "sync"); err != nil {
		t.Fatal(err)
	}
	got = runDoctorAccess(t, pveclient.Permissions{"/": {"VM.Monitor": true}})
	if c := got["access.drift"]; c.Status != doctor.Pass {
		t.Errorf("after sync drift = %+v (web1 has %v)", c, guests.managed(101))
	}
}
