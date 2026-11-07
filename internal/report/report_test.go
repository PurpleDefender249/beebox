package report

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadPrune(t *testing.T) {
	st := Store{Dir: filepath.Join(t.TempDir(), "reports")}
	base := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		r := &Report{Schema: Schema, LastSeen: base.Add(time.Duration(i) * time.Hour), Verdict: Finding{ID: "power-loss", Title: "t"}}
		r.ID = MakeID(r.LastSeen, "abcdef0123456789abcdef0123456789")
		if _, err := st.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	ids, _ := st.IDs()
	if len(ids) != 5 {
		t.Fatalf("%d ids", len(ids))
	}
	latest, err := st.Latest()
	if err != nil || latest.ID != ids[4] {
		t.Fatalf("latest = %v, %v", latest, err)
	}
	if _, err := st.Load("20261104-12"); err != nil {
		t.Fatalf("prefix load: %v", err)
	}
	if _, err := st.Load("2026"); err == nil {
		t.Fatal("ambiguous prefix should fail")
	}
	if _, err := st.Load("../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("traversal attempt returned %v", err)
	}
	if err := st.Prune(2); err != nil {
		t.Fatal(err)
	}
	ids, _ = st.IDs()
	if len(ids) != 2 || ids[1] != latest.ID {
		t.Fatalf("after prune: %v", ids)
	}
	info, err := os.Stat(filepath.Join(st.Dir, ids[0]+".json"))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("report mode = %v, %v", info.Mode(), err)
	}
}

func TestRejectsNewerSchema(t *testing.T) {
	if _, err := Decode([]byte(`{"schema": 99}`)); err == nil {
		t.Fatal("expected schema error")
	}
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"20261104-231100-5c0ffee0", "20261104-231100-unknown"} {
		if !ValidID(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "x", "../20261104-231100-5c0ffee0", "20261104-231100-5c0ffee0/..", "20261104-231100-UPPER"} {
		if ValidID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
