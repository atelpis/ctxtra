package e2e

import (
	"os"
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// Another tool's session-start hook (Orca adds one, for example) keeps
// working next to ctxtra's, and `setup --remove` leaves it working.
func TestCoexist(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		s.Marked("ctx/me.md", "PERSONAL")
		s.Config(global("me", "~/ctx/me.md"))
		otherTool(s, a.Name())
		s.Setup()

		r := s.Run(repo, driver.Options{})
		r.First(mock.Main).Has("OTHERTOOL", "PERSONAL")

		s.Ctxtra("setup", "--remove", "--agent", a.Name())
		r = s.Run(repo, driver.Options{})
		r.First(mock.Main).Has("OTHERTOOL").Lacks("PERSONAL")
	})
}

// otherTool installs another tool's session-start hook that adds
// CTXMARK-OTHERTOOL, before ctxtra is set up.
func otherTool(s *Session, agent string) {
	s.Marked("other-tool/context.md", "OTHERTOOL")
	path := s.Path("other-tool/context.md")
	switch agent {
	case "claude", "codex":
		// Plain stdout is added as context by Claude and Codex.
		cmd := `/bin/sh -c 'cat >/dev/null; /bin/cat ` + path + `'`
		file := map[string]string{"claude": ".claude/settings.json", "codex": ".codex/hooks.json"}[agent]
		s.Write(file, `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "`+cmd+`"}]}]}}`)
	case "pi":
		s.Write(".pi/agent/extensions/other-tool.ts", `import { readFileSync } from "node:fs";
export default function (pi: any) {
	pi.on("before_agent_start", async (event: any) => {
		const opts = event.systemPromptOptions;
		if (!opts) return;
		opts.contextFiles = [...(opts.contextFiles ?? []), { path: "`+path+`", content: readFileSync("`+path+`", "utf8") }];
	});
}
`)
	default:
		s.t.Fatalf("otherTool: no hook for %s", agent)
	}
	if _, err := os.Stat(s.Path("other-tool/context.md")); err != nil {
		s.t.Fatal(err)
	}
}
