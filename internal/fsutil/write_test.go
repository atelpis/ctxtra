package fsutil

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Two writes in the same second keep both old versions: the first backup
// is the original file, which must never be lost.
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
