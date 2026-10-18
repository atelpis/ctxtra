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
