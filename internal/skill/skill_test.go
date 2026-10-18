package skill

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/atelpis/ctxtra"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestRefresh(t *testing.T) {
	testenv.Home(t)
	file := filepath.Join(paths.SkillDir(), "SKILL.md")
	want, _ := fs.ReadFile(ctxtra.Skill(), "SKILL.md")

	// Not installed: nothing is written.
	if err := Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("Refresh installed the skill")
	}

	if changed, err := Write(); err != nil || !changed {
		t.Fatalf("Write: %v %v", changed, err)
	}
	if changed, err := Write(); err != nil || changed {
		t.Fatalf("Write again: %v %v", changed, err)
	}

	// From another version: Refresh writes this binary's.
	os.WriteFile(file, []byte("old"), 0o644)
	if err := Refresh(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file); string(got) != string(want) {
		t.Errorf("after Refresh:\n%s", got)
	}
}

// Codex reads a skill file with a shell command and shows the model at most
// 10,000 bytes of its output: the middle of a longer file is cut. Every file
// stays under that, with room to spare.
func TestFileSizes(t *testing.T) {
	const max = 9000
	fs.WalkDir(ctxtra.Skill(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, _ := fs.ReadFile(ctxtra.Skill(), p); len(b) > max {
			t.Errorf("%s is %d bytes; keep each skill file under %d, and move details to references/", p, len(b), max)
		}
		return nil
	})
}
