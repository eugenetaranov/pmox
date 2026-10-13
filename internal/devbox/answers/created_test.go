package answers

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCreatedMatchesPicker(t *testing.T) {
	state := t.TempDir()
	for _, c := range []Created{{"file", "/home/ada/.mcp/github/env"}, {"gh_login", ""}, {"file", "/home/ada/.mcp/github/env"}} {
		if err := AddCreated(state, c); err != nil {
			t.Fatal(err)
		}
	}
	if got := ReadCreated(state); len(got) != 2 {
		t.Fatalf("created = %+v, want each once", got)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	// picker.py reads what Go wrote, and adds to it.
	if out, err := exec.Command("python3", "../../bootstrap/picker.py", "created", "add", state, "registry", "ghcr.io").CombinedOutput(); err != nil {
		t.Fatalf("picker.py add: %v\n%s", err, out)
	}
	out, err := exec.Command("python3", "../../bootstrap/picker.py", "created", "list", state).Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := "file\t/home/ada/.mcp/github/env\ngh_login\t\nregistry\tghcr.io\n"; string(out) != want {
		t.Errorf("picker.py list = %q, want %q", out, want)
	}
	if got := ReadCreated(state); len(got) != 3 || !strings.HasPrefix(got[2].Value, "ghcr") {
		t.Errorf("Go read back %+v", got)
	}
}
