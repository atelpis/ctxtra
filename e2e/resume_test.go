package e2e

import (
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// A resumed session has the current version of the bound files, once. The
// hook runs again on resume: Copilot doesn't keep the session-start hook's
// text in the session, and Muse replaces the text from the first run.
func TestResume(t *testing.T) {
	forEachAgent(t, []driver.Cap{driver.Resume}, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		s.Marked("ctx/me.md", "BEFORE")
		s.Config(global("me", "~/ctx/me.md"))
		s.Setup()
		s.Run(repo, driver.Options{}).First(mock.Main).Has("BEFORE")
		s.Marked("ctx/me.md", "AFTER")
		r := s.Run(repo, driver.Options{Resume: true})
		r.First(mock.Main).Has("AFTER").Lacks("BEFORE").Contains(mock.Reply)
	})
}
