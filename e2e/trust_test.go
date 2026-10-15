package e2e

import (
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// Codex runs a hook only after the user trusts it: without trust, the
// session starts normally and gets nothing from ctxtra. Trusting through
// /hooks needs the interactive TUI and isn't covered here.
func TestUntrustedHookAddsNothing(t *testing.T) {
	forEachAgent(t, []driver.Cap{driver.Untrusted}, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", map[string]string{"AGENTS.md": "TEAM"})
		s.Marked("ctx/me.md", "PERSONAL")
		s.Config(global("me", "~/ctx/me.md"))
		s.Setup()
		r := s.Run(repo, driver.Options{Untrusted: true})
		r.First(mock.Main).Has("TEAM").Lacks("PERSONAL")
		if log := s.Log(); len(log) > 0 {
			t.Errorf("the hook ran without trust: %+v", log)
		}
	})
}
