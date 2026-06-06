package repository

import "testing"

func TestSaveDraftDedupIdenticalBody(t *testing.T) {
	r := New(openMem(t))
	if err := r.Create("dd", "DD", "yoga"); err != nil {
		t.Fatalf("create: %v", err)
	}
	w := sampleWorkflow("dd")
	w.Name = "v1"
	if _, err := r.SaveDraft("dd", w, "yoga", "first"); err != nil {
		t.Fatalf("save1: %v", err)
	}
	if _, err := r.SaveDraft("dd", w, "yoga", "dup"); err != nil {
		t.Fatalf("save2: %v", err)
	}
	vers, err := r.Versions("dd")
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(vers) != 1 {
		t.Fatalf("identical body should keep 1 draft snapshot, got %d", len(vers))
	}
	w.Name = "v2"
	if _, err := r.SaveDraft("dd", w, "yoga", "changed"); err != nil {
		t.Fatalf("save3: %v", err)
	}
	vers, _ = r.Versions("dd")
	if len(vers) != 2 {
		t.Fatalf("changed body should add a snapshot, got %d", len(vers))
	}
}

func TestDeleteVersion(t *testing.T) {
	r := New(openMem(t))
	if err := r.Create("dv", "DV", "yoga"); err != nil {
		t.Fatalf("create: %v", err)
	}
	w := sampleWorkflow("dv")
	w.Name = "a"
	id1, err := r.SaveDraft("dv", w, "yoga", "a")
	if err != nil {
		t.Fatalf("save a: %v", err)
	}
	w.Name = "b"
	id2, err := r.SaveDraft("dv", w, "yoga", "b")
	if err != nil {
		t.Fatalf("save b: %v", err)
	}
	if err := r.DeleteVersion("dv", id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	vers, _ := r.Versions("dv")
	if len(vers) != 1 || vers[0].ID != id2 {
		t.Fatalf("after delete want 1 row (id %d), got %d rows", id2, len(vers))
	}
	if err := r.DeleteVersion("dv", 99999); err == nil {
		t.Fatal("delete non-existent should error")
	}
	if err := r.DeleteVersion("other", id2); err == nil {
		t.Fatal("delete with mismatched workflow id should error")
	}
}

func TestClearVersions(t *testing.T) {
	r := New(openMem(t))
	if err := r.Create("cv", "CV", "yoga"); err != nil {
		t.Fatalf("create: %v", err)
	}
	w := sampleWorkflow("cv")
	for _, n := range []string{"a", "b", "c"} {
		w.Name = n
		if _, err := r.SaveDraft("cv", w, "yoga", n); err != nil {
			t.Fatalf("save %s: %v", n, err)
		}
	}
	n, err := r.ClearVersions("cv")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n != 3 {
		t.Fatalf("clear should delete 3 rows, got %d", n)
	}
	vers, _ := r.Versions("cv")
	if len(vers) != 0 {
		t.Fatalf("after clear want 0 rows, got %d", len(vers))
	}
}
