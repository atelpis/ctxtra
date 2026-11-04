package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
)

// LoadSource loads files, binding files in the folder dir, the way sessions
// read them from a pinned source called name, with dir in place of the
// accepted commit. It reads no config, store or clone mappings, so the
// result doesn't depend on the user's own setup. Each file is read with
// the limits of a session of its own: users may include any one of them.
// For source check: problems after which a binding still applies are in
// Warnings, not Problems.
func LoadSource(dir, name string, files []string) *Config {
	c := &Config{Sources: []Source{{Name: name, dir: dir}}, checking: true}
	c.Trust = c.policy()
	for _, f := range files {
		c.BindingFiles = append(c.BindingFiles, f)
		c.loadBindings(f, fsutil.NewReader(MaxFileBytes, MaxSessionBytes), &c.Sources[0])
	}
	return c
}

// LeftOut says why sessions don't get the file p of the folder dir when the
// folder is a pinned source: the store has no path that CheckPath refuses,
// such as one in .git, and leaves out symlinks and nested repositories
// (submodules among them). "" when p isn't left out, also when it doesn't
// exist: a missing file is reported when it is read.
func LeftOut(dir, p string) string {
	rel, ok := paths.Rel(dir, p)
	if !ok || rel == "." {
		return ""
	}
	if err := source.CheckPath(rel); err != nil {
		return "is not a path a pinned source can have"
	}
	cur := dir
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return ""
		}
		at, _ := paths.Rel(dir, cur)
		switch {
		case info.Mode()&fs.ModeSymlink != 0 && cur == p:
			return "is a symlink, which a pinned source leaves out"
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Sprintf("is under the symlink %s, which a pinned source leaves out", at)
		case info.IsDir() && source.Exists(filepath.Join(cur, ".git")):
			return fmt.Sprintf("is in %s, a nested repository or submodule, which a pinned source leaves out", at)
		}
	}
	return ""
}
