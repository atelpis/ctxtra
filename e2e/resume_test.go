package e2e

import (
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// A resumed session has the bound files once. Copilot doesn't keep the
// session-start hook's text in the session, and runs the hook again on
// resume.
func TestResume(t *testing.T) {
	forEachAgent(t, []driver.Cap{driver.Resume}, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		s.Marked("ctx/me.md", "PERSONAL")
		s.Config(global("me", "~/ctx/me.md"))
		s.Setup()
		s.Run(repo, driver.Options{}).First(mock.Main).Has("PERSONAL")
		r := s.Run(repo, driver.Options{Resume: true})
		r.First(mock.Main).Has("PERSONAL").Contains(mock.Reply)
	})
}
