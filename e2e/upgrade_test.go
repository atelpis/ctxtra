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

// oldSetup is the config that the previous version of ctxtra wrote for the
// agents whose hooks of that version get less text, by file in the home
// folder: testdata/upgrade has the files as it wrote them, with CTXTRA_BIN
// for the binary.
var oldSetup = map[string]map[string]string{
	"claude": {".claude/settings.json": "claude-settings.json"},
	"codex":  {".codex/hooks.json": "codex-hooks.json"},
	"muse":   {".config/muse/settings.json": "muse-settings.json"},
	"hermes": {".hermes/plugins/ctxtra/__init__.py": "hermes-plugin.py", ".hermes/plugins/ctxtra/plugin.yaml": "hermes-plugin.yaml"},
}

// After an upgrade without ctxtra setup, the hooks of the previous version
// run this one: they get as much as they got then, with whole files dropped
// from the end, never a cut file, and the log and the user are told to run
// setup.
func TestUpgradeWithoutSetup(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		files, ok := oldSetup[a.Name()]
		if !ok {
			t.Skip("the previous version's hooks get the same text")
		}
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		small := writeBig(s, "ctx/small.md", "S", 3_000, false)
		big := writeBig(s, "ctx/big.md", "B", 30_000, false)
		s.Config(global("me", "~/ctx/small.md", "~/ctx/big.md"))
		for rel, name := range files {
			b, err := os.ReadFile(filepath.Join("testdata", "upgrade", name))
			if err != nil {
				t.Fatal(err)
			}
			s.Write(rel, strings.ReplaceAll(string(b), "CTXTRA_BIN", s.Bin))
		}
		r := s.Run(repo, driver.Options{})
		text := r.First(mock.Main).text
		complete(t, "main[first]", text, small)
		absent(t, "main[first]", text, big)
		logged := false
		for _, e := range s.Log() {
			logged = logged || (slices.ContainsFunc(e.Dropped, func(p string) bool { return strings.HasSuffix(p, "/ctx/big.md") }) &&
				slices.ContainsFunc(e.Problems, func(p string) bool { return strings.Contains(p, "run ctxtra setup") }))
		}
		if !logged {
			t.Errorf("ctxtra's log doesn't list big.md as dropped and ask to run setup: %+v", s.Log())
		}
		if driver.Has(a.Driver, driver.UserMessage) {
			msgs := strings.Join(a.UserMessages(s.Home, r.Stdout), "\n")
			if !strings.Contains(msgs, "dropped big.md") || !strings.Contains(msgs, "run ctxtra setup") {
				t.Errorf("user messages %q don't say that big.md was dropped and to run setup", msgs)
			}
		}
	})
}
