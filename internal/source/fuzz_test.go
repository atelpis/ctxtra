package source

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/paths"
)

func FuzzStorePath(f *testing.F) {
	for _, s := range []string{"rules.md", "skills/go/SKILL.md", "../outside", "a/../../outside", "/absolute", ".git/config", "a/.GiT/hooks/x", "a\\b", "a\x00b", "a\u202eb"} {
		f.Add(s)
	}
	root := filepath.Join(f.TempDir(), "generation")
	f.Fuzz(func(t *testing.T, p string) {
		if CheckPath(p) != nil {
			return
		}
		if rel, ok := paths.Rel(root, filepath.Join(root, filepath.FromSlash(p))); !ok || rel == "." {
			t.Fatalf("accepted path escapes the generation: %q", p)
		}
		for _, part := range strings.Split(p, "/") {
			if strings.EqualFold(part, ".git") {
				t.Fatalf("accepted git metadata path: %q", p)
			}
		}
	})
}
