package target

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var threeVMs = []VM{
	{Name: "web1", VMID: 101, Status: "running", IP: "10.0.0.1"},
	{Name: "web2", VMID: 102, Status: "running", IP: "10.0.0.2"},
	{Name: "db1", VMID: 103, Status: "stopped"},
}

func defMnt(prev []string) string { return "/mnt/src" }

func typeStr(f *Form, s string) {
	for _, r := range s {
		send(f, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// send delivers msg and runs any command it returns (a remote listing)
// synchronously, as the program would.
func send(f *Form, msg tea.Msg) {
	_, cmd := f.Update(msg)
	for cmd != nil {
		m := cmd()
		if m == nil {
			return
		}
		if _, isQuit := m.(tea.QuitMsg); isQuit {
			return
		}
		_, cmd = f.Update(m)
	}
}

func key(f *Form, t tea.KeyType) { send(f, tea.KeyMsg{Type: t}) }

func newRemote(vms []VM) (*Remote, *Form) {
	r := &Remote{Title: "Remote target", VMs: vms, DefaultPath: defMnt}
	return r, New(context.Background(), r)
}

func TestTypingNarrowsList(t *testing.T) {
	r, f := newRemote(threeVMs)
	if got := r.visible(); len(got) != 3 {
		t.Fatalf("empty value lists %d VMs, want 3", len(got))
	}
	if g := r.ghost(); g != "web1:/mnt/src" {
		t.Errorf("ghost on empty value = %q", g)
	}
	typeStr(f, "we")
	if got := r.visible(); len(got) != 2 || got[0].Name != "web1" || got[1].Name != "web2" {
		t.Errorf("visible after 'we' = %v", got)
	}
}

func TestTabUniqueVMThenDefaultPath(t *testing.T) {
	r, f := newRemote(threeVMs)
	typeStr(f, "db")
	key(f, tea.KeyTab)
	if v := r.in.str(); v != "db1:" {
		t.Fatalf("after tab = %q, want db1:", v)
	}
	if g := r.ghost(); g != "/mnt/src" {
		t.Errorf("ghost = %q, want the default path", g)
	}
	key(f, tea.KeyTab)
	if v := r.in.str(); v != "db1:/mnt/src" {
		t.Errorf("second tab = %q", v)
	}
}

func TestTabCommonPrefixThenCycle(t *testing.T) {
	r, f := newRemote([]VM{{Name: "web1", VMID: 1}, {Name: "web2", VMID: 2}, {Name: "db", VMID: 3}})
	typeStr(f, "w")
	key(f, tea.KeyTab)
	if v := r.in.str(); v != "web" {
		t.Fatalf("tab extends to common prefix: got %q", v)
	}
	key(f, tea.KeyTab)
	first := r.in.str()
	key(f, tea.KeyTab)
	second := r.in.str()
	if first == second || !strings.HasPrefix(first, "web") || !strings.HasPrefix(second, "web") {
		t.Errorf("tab should cycle web matches: %q then %q", first, second)
	}
}

func TestArrowsReplaceVMKeepPath(t *testing.T) {
	r, f := newRemote(threeVMs)
	typeStr(f, "web1:/srv/data")
	key(f, tea.KeyDown)
	if v := r.in.str(); v != "web2:/srv/data" {
		t.Errorf("down = %q, want web2:/srv/data", v)
	}
	key(f, tea.KeyUp)
	if v := r.in.str(); v != "web1:/srv/data" {
		t.Errorf("up = %q, want web1:/srv/data", v)
	}
}

func TestBackspaceIntoVMPart(t *testing.T) {
	r, f := newRemote(threeVMs)
	typeStr(f, "web1:/srv/data")
	key(f, tea.KeyHome)
	for i := 0; i < 4; i++ {
		key(f, tea.KeyRight)
	}
	key(f, tea.KeyBackspace)
	typeStr(f, "2")
	if v := r.in.str(); v != "web2:/srv/data" {
		t.Fatalf("value = %q", v)
	}
	if r.VMs[r.hi].Name != "web2" {
		t.Errorf("highlight = %s, want web2", r.VMs[r.hi].Name)
	}
}

func TestSingleVMOpensWithItsName(t *testing.T) {
	r, f := newRemote(threeVMs[:1])
	if v := r.in.str(); v != "web1:" {
		t.Fatalf("single VM opens as %q, want web1:", v)
	}
	if r.showList() {
		t.Error("single VM must not show the list")
	}
	key(f, tea.KeyEnter)
	if !f.Done() {
		t.Fatal("enter should submit")
	}
	if vm, p := r.Result(); vm.Name != "web1" || p != "/mnt/src" {
		t.Errorf("result = %s:%s", vm.Name, p)
	}
}

func TestSingleVMListReturnsWhenEdited(t *testing.T) {
	r, f := newRemote(threeVMs[:1])
	key(f, tea.KeyHome)
	key(f, tea.KeyDelete)
	if !r.showList() {
		t.Error("editing the VM name should show the list again")
	}
}

func TestUnknownVMIsRefused(t *testing.T) {
	r, f := newRemote(threeVMs)
	typeStr(f, "web9:/srv")
	key(f, tea.KeyEnter)
	if f.Done() {
		t.Fatal("unknown VM must not submit")
	}
	if !strings.Contains(r.err, "no pmox VM named web9") || r.in.str() != "web9:/srv" {
		t.Errorf("err=%q value=%q", r.err, r.in.str())
	}
}

func TestEmptyPathUsesDefault(t *testing.T) {
	r, f := newRemote(threeVMs)
	typeStr(f, "web1:")
	key(f, tea.KeyEnter)
	if vm, p := r.Result(); !f.Done() || vm.Name != "web1" || p != "/mnt/src" {
		t.Errorf("done=%v result=%s:%s", f.Done(), vm.Name, p)
	}
}

func TestRelativePathAccepted(t *testing.T) {
	r, f := newRemote(threeVMs)
	typeStr(f, "web1:project/pmox")
	key(f, tea.KeyEnter)
	if _, p := r.Result(); !f.Done() || p != "project/pmox" {
		t.Errorf("done=%v path=%q", f.Done(), p)
	}
}

func TestRemoteCompletionAndCache(t *testing.T) {
	calls := 0
	r := &Remote{Title: "t", VMs: threeVMs, List: func(_ context.Context, vm VM, dir string) ([]string, error) {
		calls++
		if vm.Name != "web1" || dir != "/opt/" {
			t.Errorf("listed %s:%q", vm.Name, dir)
		}
		return []string{"app/", "bin/", "readme"}, nil
	}}
	f := New(context.Background(), r)
	typeStr(f, "web1:/opt/a")
	key(f, tea.KeyTab)
	if v := r.in.str(); v != "web1:/opt/app/" {
		t.Fatalf("after tab = %q", v)
	}
	key(f, tea.KeyCtrlW)
	typeStr(f, "b")
	key(f, tea.KeyTab)
	if v := r.in.str(); v != "web1:/opt/bin/" || calls != 1 {
		t.Errorf("value=%q listings=%d (want cached)", v, calls)
	}
}

func TestRelativeRemoteCompletionListsHome(t *testing.T) {
	r := &Remote{Title: "t", VMs: threeVMs, List: func(_ context.Context, _ VM, dir string) ([]string, error) {
		if dir != "" {
			t.Errorf("relative completion listed %q, want the home dir", dir)
		}
		return []string{"project/"}, nil
	}}
	f := New(context.Background(), r)
	typeStr(f, "web1:pro")
	key(f, tea.KeyTab)
	if v := r.in.str(); v != "web1:project/" {
		t.Errorf("value = %q", v)
	}
}

func TestRemoteCompletionStoppedVM(t *testing.T) {
	r := &Remote{Title: "t", VMs: threeVMs, List: func(context.Context, VM, string) ([]string, error) {
		t.Fatal("must not list a stopped VM")
		return nil, nil
	}}
	f := New(context.Background(), r)
	typeStr(f, "db1:/v")
	key(f, tea.KeyTab)
	if v := r.in.str(); v != "db1:/v" || !strings.Contains(r.hint, "stopped") {
		t.Errorf("value=%q hint=%q", v, r.hint)
	}
}

func TestRemoteCompletionError(t *testing.T) {
	r := &Remote{Title: "t", VMs: threeVMs, List: func(context.Context, VM, string) ([]string, error) {
		return nil, errors.New("timed out")
	}}
	f := New(context.Background(), r)
	typeStr(f, "web1:/x")
	key(f, tea.KeyTab)
	if !strings.Contains(r.hint, "no remote completion") {
		t.Errorf("hint = %q", r.hint)
	}
	typeStr(f, "y")
	if r.in.str() != "web1:/xy" {
		t.Error("typing must keep working after a failed listing")
	}
}

func TestLocalDefaultAndDirCompletion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "srv.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	l := &Local{Title: "Local path", DirsOnly: true}
	f := New(context.Background(), l)
	typeStr(f, "./sr")
	key(f, tea.KeyTab)
	if v := l.in.str(); v != "./src/" {
		t.Fatalf("dirs-only tab = %q, want ./src/", v)
	}

	l2 := &Local{Title: "Local path"}
	f2 := New(context.Background(), l2)
	key(f2, tea.KeyEnter)
	if !f2.Done() || l2.Result() != "." {
		t.Errorf("empty enter: done=%v result=%q", f2.Done(), l2.Result())
	}
}

func TestEscGoesBackThenAborts(t *testing.T) {
	l := &Local{Title: "Local"}
	r := &Remote{Title: "Remote", VMs: threeVMs, DefaultPath: func(prev []string) string {
		return "/mnt/" + filepath.Base(prev[0])
	}}
	f := New(context.Background(), l, r)
	typeStr(f, "src")
	l.in.set(".") // a directory that exists
	key(f, tea.KeyEnter)
	if f.i != 1 || r.def != "/mnt/." {
		t.Fatalf("i=%d def=%q", f.i, r.def)
	}
	key(f, tea.KeyEsc)
	if f.i != 0 {
		t.Fatal("esc should return to the local field")
	}
	key(f, tea.KeyEsc)
	if !f.aborted {
		t.Error("esc on the first field should abort")
	}
}
