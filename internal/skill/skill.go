// Package skill writes the ctxtra skill of this binary to
// ~/.ctxtra/skills/ctxtra, where a binding in the user's config adds it to
// sessions like any other skill.
package skill

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/atelpis/ctxtra"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Write writes every file of the skill that differs from this binary's, and
// reports whether it changed any. The folder is ctxtra's: changed files get
// no backup.
func Write() (bool, error) {
	changed := false
	err := fs.WalkDir(ctxtra.Skill(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want, err := fs.ReadFile(ctxtra.Skill(), p)
		if err != nil {
			return err
		}
		dest := filepath.Join(paths.SkillDir(), filepath.FromSlash(p))
		if got, err := os.ReadFile(dest); err == nil && bytes.Equal(got, want) {
			return nil
		}
		changed = true
		return fsutil.WriteFile(dest, want, 0o644)
	})
	return changed, err
}

// Refresh brings an installed skill to this binary's version. It does
// nothing when the skill isn't installed. The hook runs it, so the skill
// follows the binary that the agents run.
func Refresh() error {
	if _, err := os.Stat(filepath.Join(paths.SkillDir(), "SKILL.md")); err != nil {
		return nil
	}
	_, err := Write()
	return err
}
