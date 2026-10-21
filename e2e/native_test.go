package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// nativeCandidates are user-level files, relative to the home folder, that
// an agent might read: the ones it reads and some it doesn't.
var nativeCandidates = map[string][]string{
	"claude":      {".claude/CLAUDE.md", ".claude/rules/r.md", ".claude/rules/sub/s.md", ".claude/AGENTS.md"},
	"codex":       {".codex/AGENTS.md", ".codex/AGENTS.override.md", ".codex/instructions.md"},
	"pi":          {".pi/agent/AGENTS.override.md", ".pi/agent/AGENTS.md", ".pi/agent/CLAUDE.md", ".pi/agent/SYSTEM.md", ".pi/agent/APPEND_SYSTEM.md", ".pi/AGENTS.md"},
	"opencode":    {".config/opencode/AGENTS.md", ".config/opencode/CLAUDE.md", ".claude/CLAUDE.md", ".opencode/AGENTS.md"},
	"copilot":     {".copilot/copilot-instructions.md", ".copilot/instructions/x.instructions.md", ".copilot/instructions/sub/y.instructions.md", ".copilot/AGENTS.md"},
	"antigravity": {".gemini/GEMINI.md", ".gemini/AGENTS.md", ".gemini/config/GEMINI.md", ".gemini/antigravity/GEMINI.md"},
	"hermes":      {".hermes/SOUL.md", ".hermes/AGENTS.md", ".hermes/HERMES.md", ".hermes/memories/USER.md", ".hermes/memories/MEMORY.md"},
	"dsh":         {".dsh/AGENTS.md", ".dsh/DSH.md", ".dsh/instructions.md"},
	"muse":        {".config/muse/AGENTS.md", ".config/muse/MUSE.md", ".muse/AGENTS.md"},
}

// nativeExcluded are files that an agent reads but ctxtra doesn't import:
// they replace the agent's own system prompt or persona, or the agent
// writes them.
var nativeExcluded = []string{".pi/agent/SYSTEM.md", ".hermes/SOUL.md", ".hermes/memories/USER.md", ".hermes/memories/MEMORY.md"}

// nativeCase leaves some candidates out, or writes them empty, or adds
// some, to check which file an agent falls back to.
type nativeCase struct {
	name                 string
	without, empty, more []string
	// caseSensitive: the case needs a disk on which AGENTS.md and AGENTS.MD
	// are two files.
	caseSensitive bool
}

// nativeCases are the cases besides the one with every candidate.
var nativeCases = map[string][]nativeCase{
	"codex": {
		{name: "without override", without: []string{".codex/AGENTS.override.md"}},
		{name: "empty override", empty: []string{".codex/AGENTS.override.md"}},
	},
	"pi": {
		{name: "AGENTS.md", without: []string{".pi/agent/AGENTS.override.md"}},
		{name: "CLAUDE.md only", without: []string{".pi/agent/AGENTS.override.md", ".pi/agent/AGENTS.md"}},
		{name: "empty override", empty: []string{".pi/agent/AGENTS.override.md"}},
		{name: "upper case", caseSensitive: true,
			without: []string{".pi/agent/AGENTS.override.md", ".pi/agent/AGENTS.md", ".pi/agent/CLAUDE.md"},
			more:    []string{".pi/agent/AGENTS.MD", ".pi/agent/CLAUDE.MD"}},
	},
}

// The files that ctxtra import lists for an agent are the user-level files
// that reach its model: a file it lists with a note that the agent doesn't
// read it now doesn't, every other one does, and so does no file it
// doesn't list, apart from nativeExcluded. The session runs outside the
// home folder, so no file reaches the model by a search up the folders.
func TestNativeFiles(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		for _, c := range append([]nativeCase{{name: "every candidate"}}, nativeCases[a.Name()]...) {
			t.Run(c.name, func(t *testing.T) { checkNativeFiles(t, a, c) })
		}
	})
}

func checkNativeFiles(t *testing.T, a Agent, c nativeCase) {
	s := newSession(t, a)
	if c.caseSensitive && !caseSensitive(t, s.Home) {
		t.Skip("the disk isn't case-sensitive")
	}
	var candidates []string
	marker := map[string]string{}
	for _, rel := range append(slices.Clone(nativeCandidates[a.Name()]), c.more...) {
		switch {
		case slices.Contains(c.without, rel):
			continue
		case slices.Contains(c.empty, rel):
			s.Write(rel, "")
		default:
			marker[rel] = strings.NewReplacer("/", "_", ".", "", "-", "_").Replace(rel)
			if strings.HasSuffix(rel, ".MD") {
				marker[rel] += "_upper"
			}
			s.Marked(rel, marker[rel])
		}
		candidates = append(candidates, rel)
	}
	s.Setup()
	listed := map[string]string{} // rel path: its line
	for _, line := range strings.Split(s.Ctxtra("import", "--no-start"), "\n") {
		path, _, ok := strings.Cut(strings.TrimSpace(line), " (")
		if ok && strings.HasPrefix(path, "~/") {
			listed[strings.TrimPrefix(path, "~/")] = line
		}
	}
	read := mock.Markers(s.Run(t.TempDir(), driver.Options{}).First(mock.Main).text)
	for _, rel := range candidates {
		line, isListed := listed[rel]
		isRead := marker[rel] != "" && slices.Contains(read, marker[rel])
		switch {
		case isListed && strings.Contains(line, "doesn't read it"):
			if isRead {
				t.Errorf("%s: listed as not read, but the model got it: %s", rel, line)
			}
		case isListed && !isRead:
			t.Errorf("%s: listed, but the model didn't get it: %s", rel, line)
		case !isListed && isRead && !slices.Contains(nativeExcluded, rel):
			t.Errorf("%s: the model got it, but ctxtra import doesn't list it", rel)
		}
	}
	for rel := range listed {
		if !slices.Contains(candidates, rel) {
			t.Errorf("listed %s, which isn't a candidate", filepath.ToSlash(rel))
		}
	}
}

// caseSensitive reports whether dir is on a disk where a.md and A.MD are two
// files.
func caseSensitive(t *testing.T, dir string) bool {
	t.Helper()
	p := filepath.Join(dir, "case-probe")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	return err != nil
}
