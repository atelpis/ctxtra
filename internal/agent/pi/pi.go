// Package pi supports Pi. Pi has no command hooks, so setup writes a small
// extension that runs ctxtra and adds its output as context files and
// native skills.
package pi

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/extfile"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Pi.
type Agent struct{}

var (
	_ agent.Agent        = Agent{}
	_ agent.PluginClient = Agent{}
	_ agent.Native       = Agent{}
	_ agent.Launcher     = Agent{}
)

func (Agent) Name() string { return "pi" }

func dir() string {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".pi", "agent")
}

func (Agent) ConfigPath() string { return filepath.Join(dir(), "extensions", "ctxtra.ts") }

func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath("pi")
	return err == nil
}

// Setup writes or removes the extension. An extension file that ctxtra
// didn't write is left alone.
func (a Agent) Setup(o agent.Options, r *agent.Report) error {
	return extfile.Setup(a.ConfigPath(), Extension(o.Binary), o, r)
}

func (a Agent) Status(bin string, known bool) agent.Status {
	return extfile.Status(a.ConfigPath(), a.Check(bin, known))
}

func (a Agent) Check(bin string, known bool) []string {
	notices, _ := extfile.CheckSetup(extfile.Ext{Path: a.ConfigPath(), Agent: a.Name(), Noun: "extension"}, bin, known)
	return notices
}

// Answer lists the files with their content, the skills by SKILL.md path
// (Pi loads them as native skills), and a summary the extension shows the
// user at session start.
func (Agent) Answer(_ string, res *resolve.Result) any {
	type file struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	files, skills := []file{}, []string{}
	if res != nil {
		for _, h := range res.Files {
			files = append(files, file{h.Path, h.Content})
		}
		for _, h := range res.Skills {
			skills = append(skills, h.Path)
		}
	}
	return map[string]any{"files": files, "skills": skills, "message": agent.Summary(res)}
}

// Delivery: Pi gets 1,200,000 characters in full (measured with Pi 1.2.0).
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "Pi", Capacity: 1_000_000}
}

// protocol is the version of the extension, which it sends as ctxtra_plugin.
const protocol = 1

func (Agent) Protocol() int { return protocol }

// Extension is the extension that runs bin.
func Extension(bin string) string {
	return extfile.Content(extfile.TypeScript, bin, protocol, extensionBody)
}

// extensionBody runs the binary asynchronously, so Pi's event loop never
// waits on it, with a 10 second timeout and room for the largest output
// ctxtra can produce (1,000,000 characters, up to 4 MB, JSON-escaped). When
// the binary can't be run, it warns the user once per session.
var extensionBody = `// Written by ctxtra setup. Adds the instruction files and skills that
// ctxtra binds to the current folder. Safe to delete; ` + "`ctxtra setup --remove`" + ` removes it.
import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";

const bin = %s;

const missing = {};

function run(ev: string, cwd: string): Promise<any> {
	return new Promise((resolve) => {
		try {
			const child = execFile(bin, ["hook", "pi", ev], { encoding: "utf8", timeout: 10000, maxBuffer: 64 * 1024 * 1024 },
				(err: any, stdout) => {
					if (err) return resolve(err.code === "ENOENT" || err.code === "EACCES" ? missing : {});
					try {
						resolve(JSON.parse(stdout));
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

export default function ctxtra(pi: any) {
	// Tells the user once per session what ctxtra added.
	pi.on("session_start", async (_event: any, ctx: any) => {
		const answer = await run("session_start", ctx.cwd);
		if (!ctx.hasUI) return;
		if (answer === missing) ctx.ui.notify(` + string(missingMessage) + `, "warning");
		else if (answer.message) ctx.ui.notify(answer.message, "info");
	});
	// Skills are loaded as native Pi skills, also available as /skill: commands.
	pi.on("resources_discover", async (event: any) => ({ skillPaths: (await run("resources_discover", event.cwd)).skills ?? [] }));
	pi.on("before_agent_start", async (event: any, ctx: any) => {
		const files: { path: string; content: string }[] = (await run("before_agent_start", ctx.cwd)).files ?? [];
		if (files.length === 0) return;
		const opts = event.systemPromptOptions;
		if (!opts) return;
		const known = new Set((opts.contextFiles ?? []).map((f: any) => f.path));
		opts.contextFiles = [...(opts.contextFiles ?? []), ...files.filter((f) => !known.has(f.path))];
	});
}
`

var missingMessage, _ = json.Marshal(agent.MissingMessage("pi"))

// contextNames are the user-level context files that Pi looks for, in its
// order: it reads the first one that exists, also an empty one.
var contextNames = []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}

// NativeFiles: the context file that Pi reads, the others with a note, and
// APPEND_SYSTEM.md, which Pi adds to its system prompt. SYSTEM.md, which
// replaces Pi's own system prompt, isn't one.
func (Agent) NativeFiles() []agent.NativeFile {
	var out []agent.NativeFile
	var seen []os.FileInfo // AGENTS.md and AGENTS.MD are one file on a case-insensitive disk
	read := ""
	for _, name := range contextNames {
		p := filepath.Join(dir(), name)
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || slices.ContainsFunc(seen, func(s os.FileInfo) bool { return os.SameFile(s, st) }) {
			continue
		}
		seen = append(seen, st)
		f, ok := agent.ReadNative(p)
		if read == "" {
			read = name
		} else {
			f.Note = "Pi doesn't read it while " + read + " exists"
		}
		if ok {
			out = append(out, f)
		}
	}
	if f, ok := agent.ReadNative(filepath.Join(dir(), "APPEND_SYSTEM.md")); ok {
		out = append(out, f)
	}
	return out
}

func (Agent) Launch(prompt string) []string { return []string{"pi", prompt} }
