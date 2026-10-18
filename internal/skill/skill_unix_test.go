//go:build unix

package skill

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestRefreshRefuses(t *testing.T) {
	testenv.Home(t)
	file := filepath.Join(paths.SkillDir(), "SKILL.md")

	// A FIFO in place of SKILL.md doesn't block; it is replaced.
	os.MkdirAll(paths.SkillDir(), 0o755)
	if err := syscall.Mkfifo(file, 0o644); err != nil {
		t.Skip(err)
	}
	if err := Refresh(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(file); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("SKILL.md after Refresh: %v %v", fi, err)
	}

	// A link in place of the folder: the folder it points to isn't changed.
	other := filepath.Join(t.TempDir(), "mine")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "SKILL.md"), []byte("mine"), 0o644)
	os.RemoveAll(paths.SkillDir())
	if err := os.Symlink(other, paths.SkillDir()); err != nil {
		t.Fatal(err)
	}
	if err := Refresh(); err == nil {
		t.Error("Refresh wrote through a link")
	}
	if got, _ := os.ReadFile(filepath.Join(other, "SKILL.md")); string(got) != "mine" {
		t.Errorf("the linked folder's SKILL.md changed:\n%s", got)
	}
}
