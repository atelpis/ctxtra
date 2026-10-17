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
	case "claude", "codex", "muse":
		// Plain stdout is added as context by Claude, Codex and Muse. Muse
		// also needs the schema version.
		cmd := `/bin/sh -c 'cat >/dev/null; /bin/cat ` + path + `'`
		hooks := `"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "` + cmd + `"}]}]}`
		switch agent {
		case "claude":
			s.Write(".claude/settings.json", "{"+hooks+"}")
		case "codex":
			s.Write(".codex/hooks.json", "{"+hooks+"}")
		case "muse":
			s.Write(".config/muse/settings.json", `{"schema_version": 1, `+hooks+"}")
		}
	case "copilot":
		// Copilot reads only JSON from a hook; the tool has a file of its
		// own next to ctxtra's.
		s.Write("other-tool/answer.json", `{"additionalContext": "CTXMARK-OTHERTOOL"}`)
		cmd := `/bin/sh -c 'cat >/dev/null; /bin/cat ` + s.Path("other-tool/answer.json") + `'`
		s.Write(".copilot/hooks/other-tool.json", `{"version": 1, "hooks": {"sessionStart": [{"type": "command", "bash": "`+cmd+`"}]}}`)
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
	case "opencode":
		s.Write(".config/opencode/plugins/other-tool.ts", `import { readFileSync } from "node:fs";
export default {
	id: "other-tool",
	async setup(ctx: any) {
		await ctx.session.hook("context", (event: any) => {
			event.system.push({ type: "text", text: readFileSync("`+path+`", "utf8") });
		});
	},
};
`)
	default:
		s.t.Fatalf("otherTool: no hook for %s", agent)
	}
	if _, err := os.Stat(s.Path("other-tool/context.md")); err != nil {
		s.t.Fatal(err)
	}
}
