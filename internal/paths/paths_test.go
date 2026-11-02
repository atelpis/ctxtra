package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTildeReal(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real, link := filepath.Join(root, "real"), filepath.Join(root, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	file := filepath.Join(real, "a", "b.md")
	outside := filepath.Join(real+"x", "b.md")
	for _, home := range []string{real, link} {
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		for p, want := range map[string]string{real: "~", file: "~/a/b.md", filepath.Join(home, "a", "b.md"): "~/a/b.md", outside: outside} {
			if got := TildeReal(p); got != want {
				t.Errorf("home %s: TildeReal(%s) = %s, want %s", home, p, got, want)
			}
		}
	}
	// Saved in the config, Tilde's result has to expand to the same text.
	if got := Tilde(file); got != file {
		t.Errorf("Tilde(%s) = %s through a symlinked home", file, got)
	}
}
