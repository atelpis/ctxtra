// Package paths resolves user paths (~, relative paths, symlinks) and knows
// where ctxtra keeps its own files.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// Home is the user's home directory, or / when it is unknown.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

// Expand replaces a leading ~ with the home directory.
func Expand(p string) string {
	if p == "~" {
		return Home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(Home(), p[2:])
	}
	return p
}

// Resolve expands ~ and makes a relative p relative to base.
func Resolve(base, p string) string {
	p = Expand(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// Real is the absolute path with symlinks resolved, or the absolute path when
// it does not exist.
func Real(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Tilde shows a path under the home directory as ~/...
func Tilde(p string) string {
	h := Home()
	if p == h {
		return "~"
	}
	if strings.HasPrefix(p, h+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(p[len(h)+1:])
	}
	return p
}

// StateDir is ~/.ctxtra: backups of changed agent configs and the hook log.
func StateDir() string { return filepath.Join(Home(), ".ctxtra") }

// BackupDir is where setup keeps a copy of every agent config it changes.
func BackupDir() string { return filepath.Join(StateDir(), "backups") }

// ConfigPath is $CTXTRA_CONFIG or ~/.config/ctxtra/config.yaml.
func ConfigPath() string {
	if p := os.Getenv("CTXTRA_CONFIG"); p != "" {
		return Expand(p)
	}
	return filepath.Join(Home(), ".config", "ctxtra", "config.yaml")
}

// LogPath is $CTXTRA_LOG or ~/.ctxtra/log.jsonl.
func LogPath() string {
	if p := os.Getenv("CTXTRA_LOG"); p != "" {
		return Expand(p)
	}
	return filepath.Join(StateDir(), "log.jsonl")
}

// Rel returns p relative to root, slash-separated, when p is root (".") or
// below it. The paths are compared by components as cleaned text, without
// resolving symlinks: ..x is a name below root, .. is not.
func Rel(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
