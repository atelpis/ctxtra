package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// The ctxtra skill that skill install binds reaches the agents like any
// other skill: listed at session start, its body only when the agent loads
// it. A scripted mock checks delivery, not whether a real model would choose
// the skill.
func TestCtxtraSkill(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "https://github.com/acme/svc", nil)
		s.Setup()
		s.Ctxtra("skill", "install", "ctxtra")
		path := filepath.Join(s.Home, ".ctxtra", "skills", "ctxtra", "SKILL.md")
		r := s.Run(repo, driver.Options{}, mock.LoadSkill("ctxtra", path))
		first := r.First(mock.Main).Contains("Set up and change ctxtra")
		if strings.Contains(first.text, "## Remote repositories") {
			t.Error("the skill's body reached the model before the agent loaded it")
		}
		r.Last(mock.Main).Contains("## Remote repositories")
	})
}
