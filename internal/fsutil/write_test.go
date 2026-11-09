package fsutil

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Two changes keep both old versions, even within the same second.
func TestWriteAtomicBackups(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"one", "two", "two"} {
		if _, _, err := WriteAtomic(path, []byte(v), backups, false); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(backups)
	var got []string
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(backups, e.Name()))
		got = append(got, string(b))
		if info, _ := e.Info(); info.Mode().Perm() != 0o600 {
			t.Errorf("backup mode %v", info.Mode())
		}
	}
	if len(got) != 2 || !contains(got, "original") || !contains(got, "one") {
		t.Errorf("backups %q", got)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Errorf("mode changed to %v", st.Mode())
	}
	if _, changed, _ := WriteAtomic(path, []byte("three"), backups, true); !changed {
		t.Error("dry run: no change reported")
	}
	if b, _ := os.ReadFile(path); string(b) != "two" {
		t.Errorf("dry run wrote %q", b)
	}
}

func contains(list []string, s string) bool {
	return slices.Contains(list, s)
}

func TestWriteIfUnchanged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.json")
	if _, _, err := WriteIfUnchanged(p, nil, []byte("a"), filepath.Join(dir, "b"), false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteIfUnchanged(p, nil, []byte("b"), filepath.Join(dir, "b"), false); err != ErrChanged {
		t.Errorf("created meanwhile: %v", err)
	}
	if _, _, err := WriteIfUnchanged(p, []byte("x"), []byte("b"), filepath.Join(dir, "b"), false); err != ErrChanged {
		t.Errorf("changed meanwhile: %v", err)
	}
	if _, changed, err := WriteIfUnchanged(p, []byte("a"), []byte("b"), filepath.Join(dir, "b"), false); err != nil || !changed {
		t.Errorf("%v %v", changed, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "b" {
		t.Errorf("%q", b)
	}
	_ = os.Remove(p)
	if _, _, err := WriteIfUnchanged(p, []byte("b"), []byte("c"), filepath.Join(dir, "b"), false); err != ErrChanged {
		t.Errorf("removed meanwhile: %v", err)
	}
}
