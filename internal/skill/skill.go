// Package skill writes ctxtra's skills of this binary to
// ~/.ctxtra/skills/<name>, where a binding in the user's config adds them to
// sessions like any other skill.
package skill

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/atelpis/ctxtra"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Base is the ctxtra skill, which lets agents set up and change ctxtra.
const Base = "ctxtra"

// Write writes every file of the skill name that differs from this
// binary's, and reports whether it changed any. The folder is ctxtra's:
// changed files get no backup. A link in place of the folder is refused, so
// the hook never writes into a folder somewhere else.
func Write(name string) (bool, error) {
	src := ctxtra.Skill(name)
	if src == nil {
		return false, fmt.Errorf("ctxtra has no skill %q", name)
	}
	dir := paths.SkillDir(name)
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		return false, fmt.Errorf("%s isn't a folder; not changed", paths.Tilde(dir))
	}
	changed := false
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.FromSlash(p))
		// A bounded read that refuses a FIFO, so the hook can't block; a
		// file it can't read is replaced.
		if got, err := fsutil.ReadBounded(dest, len(want)); err == nil && bytes.Equal(got, want) {
			return nil
		}
		changed = true
		return fsutil.WriteFile(dest, want, 0o644)
	})
	return changed, err
}

// Refresh brings each installed skill to this binary's version. It skips
// the skills that aren't installed. The hook runs it, so the skills follow
// the binary that the agents run.
func Refresh() error {
	var errs []error
	for _, name := range ctxtra.SkillNames() {
		if _, err := os.Stat(filepath.Join(paths.SkillDir(name), "SKILL.md")); err != nil {
			continue
		}
		if _, err := Write(name); err != nil {
			errs = append(errs, fmt.Errorf("skill %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}
