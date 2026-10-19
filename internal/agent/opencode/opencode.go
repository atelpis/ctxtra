// Package opencode supports OpenCode 2. OpenCode has no command hooks, so
// setup writes a small plugin that runs ctxtra, adds the files to the system
// prompt and the skills as native skills.
package opencode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/extfile"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is OpenCode.
type Agent struct{}

var _ agent.Agent = Agent{}

func (Agent) Name() string { return "opencode" }

// dir is OpenCode's global config folder, found the way OpenCode finds it.
func dir() string {
	if d := os.Getenv("OPENCODE_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "opencode")
	}
	return filepath.Join(paths.Home(), ".config", "opencode")
}

// ConfigPath is in the global plugins folder, which OpenCode loads without
// a config entry.
func (Agent) ConfigPath() string { return filepath.Join(dir(), "plugins", "ctxtra.ts") }

// Detect also checks ~/.opencode, where the install script puts the binary
// without adding it to every PATH.
func (Agent) Detect() bool {
	for _, d := range []string{dir(), filepath.Join(paths.Home(), ".opencode")} {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return true
		}
	}
	_, err := exec.LookPath("opencode")
	return err == nil
}

// Setup writes or removes the plugin. A plugin file that ctxtra didn't write
// is left alone.
func (a Agent) Setup(o agent.Options, r *agent.Report) error {
	return extfile.Setup(a.ConfigPath(), Plugin(o.Binary), o, r)
}

func (a Agent) Status(bin string, known bool) agent.Status {
	return extfile.Status(a.ConfigPath(), a.Check(bin, known))
}

func (a Agent) Check(bin string, known bool) []string {
	notices, _ := extfile.CheckSetup(extfile.Ext{Path: a.ConfigPath(), Agent: a.Name(), Noun: "plugin"}, bin, known)
	return notices
}

// Answer is the files as one text for the system prompt, and the skills with
// their SKILL.md body: OpenCode's skill tool gives the model the body the
// plugin registered, it doesn't read the file. OpenCode has no way to show a
// message from a plugin, so there is no summary.
func (Agent) Answer(_ string, res *resolve.Result) any {
	type skill struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Path        string `json:"path"`
		Content     string `json:"content"`
	}
	skills := []skill{}
	if res != nil {
		for _, h := range res.Skills {
			skills = append(skills, skill{h.Name, h.Description, h.Path, h.Content})
		}
	}
	return map[string]any{"text": res.RenderFiles(), "skills": skills}
}

// Delivery: OpenCode gets 1,200,000 characters in full (measured with OpenCode
// 2.0.29).
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "OpenCode", Capacity: 1_000_000}
}

// protocol is the version of the plugin, which it sends as ctxtra_plugin.
const protocol = 1

// Plugin is the plugin that runs bin.
func Plugin(bin string) string { return extfile.Content(extfile.TypeScript, bin, protocol, pluginBody) }

// pluginBody runs the binary asynchronously, like Pi's extension, with a 10
// second timeout and room for the largest output ctxtra can produce.
//
// The context hook runs before every model request of a session, subagents'
// sessions included. The plugin asks ctxtra once per session, for the
// session's folder, and adds the same text to every request, so the system
// prompt stays the same through the session. ctxtra logs a subagent's
// session as a "subagent" event, other sessions as "context".
//
// OpenCode runs one plugin instance per folder, often in a long-running
// background service, so the skills found at setup are updated when a new
// session gets a different list; OpenCode applies them from the next
// request.
var pluginBody = `// Written by ctxtra setup. Adds the instruction files and skills that
// ctxtra binds to the session's folder. Safe to delete; ` + "`ctxtra setup --remove`" + ` removes it.
import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";

const bin = %s;

type Skill = { name: string; description: string; path: string; content: string };
type Answer = { text?: string; skills?: Skill[] };

function run(ev: string, cwd: string): Promise<Answer> {
	return new Promise((resolve) => {
		try {
			const child = execFile(bin, ["hook", "opencode", ev], { encoding: "utf8", timeout: 10000, maxBuffer: 64 * 1024 * 1024 },
				(err, stdout) => {
					if (err) return resolve({});
					try {
						resolve(JSON.parse(stdout) ?? {});
					} catch {
						resolve({});
					}
				});
			child.stdin?.on("error", () => {});
			child.stdin?.end(JSON.stringify({ cwd, ctxtra_plugin: ` + strconv.Itoa(protocol) + ` }));
		} catch {
			resolve({});
		}
	});
}

// How many sessions' answers the plugin keeps; the oldest goes first.
const maxSessions = 256;

// The plugin never throws: OpenCode would show a plugin error, or fail the
// request. Without an answer from ctxtra (no binary, timeout) it adds
// nothing, keeps the skills it has, and asks again on the next request.
export default {
	id: "ctxtra",
	async setup(ctx: any) {
		try {
			const here: string = ctx.location?.directory ?? process.cwd();
			let skills: Skill[] = (await run("setup", here)).skills ?? [];
			// A skill of the user's own with the same name wins.
			await ctx.skill.transform((editor: any) => {
				for (const s of skills) {
					if (!editor.get(s.name)) editor.add({ id: s.name, ...s });
				}
			});
			const sessions = new Map<string, Promise<Answer>>();
			const answer = (sessionID: string): Promise<Answer> => {
				let p = sessions.get(sessionID);
				if (!p) {
					p = (async () => {
						let info: any;
						try {
							info = await ctx.session.get({ sessionID });
						} catch {}
						const a = await run(info?.parentID ? "subagent" : "context", info?.location?.directory ?? here);
						if (a.text === undefined) sessions.delete(sessionID);
						return a;
					})();
					sessions.set(sessionID, p);
					if (sessions.size > maxSessions) sessions.delete(sessions.keys().next().value!);
				}
				return p;
			};
			await ctx.session.hook("context", async (event: any) => {
				try {
					const a = await answer(event.sessionID);
					if (a.text) event.system.push({ type: "text", text: a.text });
					if (a.skills && JSON.stringify(a.skills) !== JSON.stringify(skills)) {
						skills = a.skills;
						await ctx.skill.reload();
					}
				} catch {}
			});
		} catch {}
	},
};
`
