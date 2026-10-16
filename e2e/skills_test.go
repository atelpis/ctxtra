package e2e

import (
	"path/filepath"
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// The model sees each bound skill's name and description, not its body;
// when the agent loads the skill, the body reaches the model. A skill bound
// to another repository isn't listed.
func TestSkills(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "https://github.com/acme/svc", nil)
		deploy := s.Skill("skills/deploy", "deploy", "DEPLOYDESC", "DEPLOYBODY")
		s.Skill("skills/other", "other", "OTHERDESC", "OTHERBODY")
		s.Config(`apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: svc}
spec:
  selector: {gitRepos: [{remote: github.com/acme/svc}]}
  skills: [~/skills/deploy]
---
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata: {name: other}
spec:
  selector: {gitRepos: [{remote: github.com/acme/other}]}
  skills: [~/skills/other]
`)
		s.Setup()
		path := filepath.Join(deploy, "SKILL.md")
		r := s.Run(repo, driver.Options{}, mock.LoadSkill("deploy", path))
		first := r.First(mock.Main).Has("DEPLOYDESC").Lacks("DEPLOYBODY", "OTHERDESC", "OTHERBODY")
		if !driver.Has(a, driver.SkillTool) {
			first.Contains(path)
		}
		r.Last(mock.Main).Has("DEPLOYBODY")
	})
}
