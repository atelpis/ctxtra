package e2e

import (
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// The bound files reach the model at session start, in config order, after
// the repository's own AGENTS.md, in a worktree outside the project folder.
// A binding for another repository adds nothing.
func TestStartup(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		s.Repo("dev/svc", "git@github.com:Acme/Svc.git", map[string]string{"AGENTS.md": "TEAM"})
		wt := s.Worktree("dev/svc", "elsewhere/svc-feature", "feature")
		s.Marked("ctx/work.md", "WORK")
		s.Marked("ctx/svc.md", "SVC")
		s.Marked("ctx/me.md", "PERSONAL")
		s.Marked("ctx/other.md", "OTHER")
		s.Config(`apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: acme}
spec:
  selector: {gitRepos: [{remote: "github.com/acme/**"}]}
  files: [~/ctx/work.md]
---
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: svc}
spec:
  selector: {dirs: [~/dev/svc]}
  files: [~/ctx/svc.md]
---
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: me}
spec:
  selector: {global: true}
  files: [~/ctx/me.md]
---
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: other}
spec:
  selector: {gitRepos: [{remote: github.com/acme/other}]}
  files: [~/ctx/other.md]
`)
		s.Setup()
		r := s.Run(wt, driver.Options{})
		r.First(mock.Main).
			Has("TEAM", "WORK", "SVC", "PERSONAL").
			Lacks("OTHER").
			Order("TEAM", "WORK", "SVC", "PERSONAL")
		s.Clean("dev/svc", "elsewhere/svc-feature")
		s.hookRan("SessionStart", "session_start", "before_agent_start", "context", "sessionStart", "pre_llm_call", "startup")
	})
}
