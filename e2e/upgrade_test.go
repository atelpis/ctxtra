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

// preV1 is the client fixtures of the versions before version 1, for the
// agents whose hooks of those versions get less text.
const preV1 = "../testdata/clients/pre-v1"

// After an upgrade without ctxtra setup, the hooks of the previous version
// run this one: they get as much as they got then, with whole files dropped
// from the end, never a cut file, and the log and the user are told to run
// setup.
func TestUpgradeWithoutSetup(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		dir := filepath.Join(preV1, a.Name())
		if _, err := os.Stat(dir); err != nil {
			t.Skip("the previous version's hooks get the same text")
		}
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		small := writeBig(s, "ctx/small.md", "S", 3_000, false)
		big := writeBig(s, "ctx/big.md", "B", 30_000, false)
		s.Config(global("me", "~/ctx/small.md", "~/ctx/big.md"))
		s.LoadClient(dir)
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
