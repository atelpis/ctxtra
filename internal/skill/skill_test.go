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
	const follow = "ctxtra-follow"
	file := filepath.Join(paths.SkillDir(Base), "SKILL.md")
	want, _ := fs.ReadFile(ctxtra.Skill(Base), "SKILL.md")

	// Not installed: nothing is written.
	if err := Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.SkillDir(Base)); err == nil {
		t.Fatal("Refresh installed the skill")
	}

	if changed, err := Write(Base); err != nil || !changed {
		t.Fatalf("Write: %v %v", changed, err)
	}
	if changed, err := Write(Base); err != nil || changed {
		t.Fatalf("Write again: %v %v", changed, err)
	}
	if _, err := Write("nope"); err == nil {
		t.Error("Write wrote a skill ctxtra doesn't have")
	}

	// From another version: Refresh writes this binary's, of the installed
	// skills only.
	os.WriteFile(file, []byte("old"), 0o644)
	if err := Refresh(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file); string(got) != string(want) {
		t.Errorf("after Refresh:\n%s", got)
	}
	if _, err := os.Stat(paths.SkillDir(follow)); err == nil {
		t.Errorf("Refresh installed %s", follow)
	}

	followFile := filepath.Join(paths.SkillDir(follow), "SKILL.md")
	os.MkdirAll(filepath.Dir(followFile), 0o755)
	os.WriteFile(followFile, []byte("old"), 0o644)
	if err := Refresh(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(followFile); string(got) == "old" {
		t.Errorf("Refresh didn't update %s", follow)
	}
}

// Codex reads a skill file with a shell command and shows the model at most
// 10,000 bytes of its output: the middle of a longer file is cut. Every file
// stays under that, with room to spare.
func TestFileSizes(t *testing.T) {
	const max = 9000
	for _, name := range ctxtra.SkillNames() {
		src := ctxtra.Skill(name)
		fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if b, _ := fs.ReadFile(src, p); len(b) > max {
				t.Errorf("%s/%s is %d bytes; keep each skill file under %d, and move details to references/", name, p, len(b), max)
			}
			return nil
		})
	}
}

// Each skill's folder name is its name, which is also the name of the
// binding that skill install adds.
func TestNames(t *testing.T) {
	names := ctxtra.SkillNames()
	if len(names) < 2 || names[0] != Base {
		t.Fatalf("skills: %v", names)
	}
	for _, name := range names {
		b, err := fs.ReadFile(ctxtra.Skill(name), "SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if want := "---\nname: " + name + "\n"; string(b[:min(len(b), len(want))]) != want {
			t.Errorf("%s/SKILL.md doesn't start with %q", name, want)
		}
	}
}
