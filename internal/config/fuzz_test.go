package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/paths"
)

func FuzzConfig(f *testing.F) {
	for _, seed := range []string{
		"", "schema: ctxtra/v1\n", "schema: ctxtra/v1\nbudget: {max-chars: -1}\n",
		"schema: ctxtra/v1\ninclude: [team.yaml]\n",
		"schema: ctxtra/v1\nsources: {team: {git: 'https://example.com/team', branch: main}}\n",
		"schema: ctxtra/v1\nbindings: [{name: x, match: {global: true}, instructions: [x.md]}]\n",
		"schema: ctxtra/v1\nbudget: &a {max-chars: 1}\ninclude: *a\n",
		"schema: ctxtra/v1\n---\nschema: ctxtra/v1\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := Parse(data)
		if err != nil {
			return
		}
		if c.MaxChars < 0 {
			t.Fatalf("negative budget accepted: %d", c.MaxChars)
		}
		if len(c.Bindings) != 0 || len(c.BindingFiles) != 0 {
			t.Fatal("Parse loaded bindings")
		}
		if strings.TrimSpace(string(data)) != "" && Check(data) != nil {
			// Empty YAML documents are accepted by Parse, but not Check.
			docs, err := parseDocs(data)
			if err != nil || len(docs) != 0 {
				t.Fatal("Parse and Check disagree on a nonempty document")
			}
		}
		for _, s := range c.Sources {
			if !ValidName(s.Name) {
				t.Fatalf("invalid source name accepted: %q", s.Name)
			}
		}
		// Exercise binding validation without resolving files from fuzz input.
		docs, _ := parseDocs(data)
		for _, d := range docs {
			if d.err == nil {
				_, _ = c.decodeBindingFile(d.node)
			}
		}
	})
}

func FuzzSourceReference(f *testing.F) {
	for _, s := range []string{"rules.md", "../rules.md", "../../outside", "/etc/passwd", "~/rules.md", "ctxtra://other/rules.md", "ctxtra://team/../outside", "nested/../../outside", "..x/rules.md"} {
		f.Add(s)
	}
	root := filepath.Join(f.TempDir(), "source")
	f.Setenv("HOME", f.TempDir())
	pinned := Source{Name: "team", Git: "https://example.com/team", Commit: strings.Repeat("a", 40)}
	f.Fuzz(func(t *testing.T, ref string) {
		c := &Config{Sources: []Source{{Name: "team", TrustedPath: root}, {Name: "other", TrustedPath: root + "-other"}}}
		p, _, err := c.ref(Scheme+"team/"+ref, root, nil)
		if err == nil {
			rel, ok := paths.Rel(root, p)
			if !ok || rel == "." {
				t.Fatalf("source reference escaped: %q -> %q", ref, p)
			}
		}
		c = &Config{Sources: []Source{pinned, {Name: "other", TrustedPath: root}}}
		p, src, err := c.ref(ref, filepath.Join(pinned.Gen(), "nested"), &pinned)
		if err == nil {
			if _, ok := paths.Rel(pinned.Gen(), p); !ok || src == nil || src.Name != pinned.Name {
				t.Fatalf("pinned binding escaped its source: %q -> %q", ref, p)
			}
		}
	})
}
