package mount

import (
	"testing"
)

func TestID(t *testing.T) {
	a := ID("/home/user/src", "/opt/app")
	if a != ID("/home/user/src", "/opt/app") {
		t.Error("ID must be deterministic")
	}
	if a == ID("/home/user/other", "/opt/app") {
		t.Error("different local paths must yield different IDs")
	}
	if a == ID("/home/user/src", "/opt/other") {
		t.Error("different remote paths must yield different IDs")
	}
}

func TestSaveListForVMFindRemove(t *testing.T) {
	dir := t.TempDir()

	r1 := Record{VMName: "web1", LocalPath: "/src/a", RemotePath: "/opt/a", PID: 111}
	r2 := Record{VMName: "web1", LocalPath: "/src/b", RemotePath: "/opt/b", PID: 222}
	r3 := Record{VMName: "web2", LocalPath: "/src/a", RemotePath: "/opt/a", PID: 333}
	for _, r := range []Record{r1, r2, r3} {
		if _, err := Save(dir, r); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	all, err := List(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("list = %d records, want 3", len(all))
	}

	web1, err := ForVM(dir, "web1")
	if err != nil {
		t.Fatalf("forVM: %v", err)
	}
	if len(web1) != 2 {
		t.Errorf("ForVM(web1) = %d, want 2", len(web1))
	}

	// Find must return the exact path pair — this is what fixes umount
	// targeting a specific mount rather than every mount for the VM.
	got, ok, err := Find(dir, "/src/b", "/opt/b")
	if err != nil || !ok {
		t.Fatalf("Find: ok=%v err=%v", ok, err)
	}
	if got.PID != 222 {
		t.Errorf("Find returned pid %d, want 222", got.PID)
	}

	if err := Remove(got); err != nil {
		t.Fatalf("remove: %v", err)
	}
	all, _ = List(dir)
	if len(all) != 2 {
		t.Errorf("after remove, list = %d, want 2", len(all))
	}
}

func TestListMissingDirIsEmpty(t *testing.T) {
	all, err := List(t.TempDir() + "/does-not-exist")
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("want 0 records, got %d", len(all))
	}
}
