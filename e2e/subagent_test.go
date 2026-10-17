package e2e

import (
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// A subagent gets the bound files too, and the main agent still has them
// once after tool calls.
func TestSubagent(t *testing.T) {
	forEachAgent(t, []driver.Cap{driver.Subagent}, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", map[string]string{"AGENTS.md": "TEAM", "internal/code.txt": "CODE"})
		s.Marked("ctx/me.md", "PERSONAL")
		s.Config(global("me", "~/ctx/me.md"))
		s.Setup()
		r := s.Run(repo, driver.Options{}, mock.Read(s.Path("dev/svc/internal/code.txt")), mock.StartSubagent())
		r.First(mock.Main).Has("TEAM", "PERSONAL")
		r.Last(mock.Main).Has("TEAM", "PERSONAL", "CODE")
		r.First(mock.Subagent).Has("PERSONAL")
		if a.Name() == "antigravity" { // a subagent is a session of its own
			s.hookRanTimes(2, "SessionStart")
		} else {
			s.hookRan("SubagentStart", "subagent", "subagentStart")
		}
	})
}

// global is a binding file with one binding that adds files to every session.
func global(name string, files ...string) string {
	y := "apiVersion: ctxtra/v1alpha1\nkind: Binding\nmetadata: {name: " + name + "}\nspec:\n  selector: {global: true}\n  files:\n"
	for _, f := range files {
		y += "    - " + f + "\n"
	}
	return y
}
