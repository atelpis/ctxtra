package e2e

import (
	"slices"
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// The agent gets ctxtra's one-line message for the user at session
// start. This checks the agent accepted the message from the hook; whether
// the TUI shows it needs an interactive run.
func TestUserMessage(t *testing.T) {
	forEachAgent(t, []driver.Cap{driver.UserMessage}, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		s.Marked("ctx/me.md", "PERSONAL")
		s.Config(global("me", "~/ctx/me.md", "~/ctx/gone.md"))
		s.Setup()
		r := s.Run(repo, driver.Options{})
		r.First(mock.Main).Has("PERSONAL")
		want := "ctxtra: added 1 file (me.md); 1 not found (run ctxtra doctor)"
		if got := a.UserMessages(r.Stdout); !slices.Equal(got, []string{want}) {
			t.Errorf("user messages %q, want [%q]", got, want)
		}
	})
}

// Outside git, only global and dirs bindings apply.
func TestOutsideGit(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		s.Marked("ctx/me.md", "PERSONAL")
		s.Marked("ctx/notes.md", "NOTES")
		s.Marked("ctx/repo.md", "REPO")
		s.Write("notes/todo.md", "plain folder\n")
		s.Config(global("me", "~/ctx/me.md") + `---
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: notes}
spec:
  selector: {dirs: [~/notes]}
  files: [~/ctx/notes.md]
---
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: repos}
spec:
  selector: {gitRepos: [{remote: "**"}]}
  files: [~/ctx/repo.md]
`)
		s.Setup()
		r := s.Run(s.Path("notes"), driver.Options{})
		r.First(mock.Main).Has("PERSONAL", "NOTES").Lacks("REPO").Order("PERSONAL", "NOTES")
	})
}
