package bootstrap

import (
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
)

func TestClockSyncStarterTemplate(t *testing.T) {
	in, err := config.RenderTemplate("ubuntu", "ssh-ed25519 AAAA test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := ClockSync(in)
	if err != nil {
		t.Fatal(err)
	}
	doc, wfs := parse(t, out)
	if doc["users"] == nil {
		t.Errorf("original keys lost: %v", doc)
	}
	if pk, _ := doc["packages"].([]any); !containsAny(pk, "chrony") {
		t.Errorf("packages = %v, want chrony", doc["packages"])
	}
	got := map[string]string{}
	for _, w := range wfs {
		got[w.Path] = string(decode(t, w.Content))
	}
	if got["/etc/modules-load.d/ptp_kvm.conf"] != "ptp_kvm\n" {
		t.Errorf("ptp_kvm module file = %q", got["/etc/modules-load.d/ptp_kvm.conf"])
	}
	conf := got[chronyPTPStaged]
	for _, w := range []string{"refclock PHC /dev/ptp0 poll 2 dpoll -2 offset 0 stratum 2 prefer", "makestep 1 -1"} {
		if !strings.Contains(conf, w) {
			t.Errorf("chrony drop-in lacks %q:\n%s", w, conf)
		}
	}
	run, _ := doc["runcmd"].([]any)
	if len(run) == 0 || run[len(run)-1] != ClockSyncCommand {
		t.Errorf("runcmd = %v, want the clock-sync command last", run)
	}
	if !strings.Contains(ClockSyncCommand, "[ -e /dev/ptp0 ]") {
		t.Errorf("drop-in installed even without /dev/ptp0: %s", ClockSyncCommand)
	}
}

func TestClockSyncKeepsPackages(t *testing.T) {
	for _, in := range []string{
		"#cloud-config\npackages: [git, chrony]\n",
		"#cloud-config\npackages:\n  - git\n  - [chrony, 4.5-1]\n",
		"#cloud-config\npackages: [git, chrony=4.5-1]\n",
	} {
		out, err := ClockSync([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		doc, _ := parse(t, out)
		pk, _ := doc["packages"].([]any)
		if len(pk) != 2 || pk[0] != "git" {
			t.Errorf("%q: packages = %v, want git and chrony once", in, pk)
		}
	}
}

func TestClockSyncRejectsBadList(t *testing.T) {
	if _, err := ClockSync([]byte("#cloud-config\npackages: chrony\n")); err == nil {
		t.Error("packages as a string accepted")
	}
}

func containsAny(list []any, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
