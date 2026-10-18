// Package dsh supports DeepSeek Harness (dsh). It has no command hooks that
// work without an extra plugin package, so setup writes a small plugin that
// runs ctxtra, and adds the row that loads it to the patch file that DSH
// applies to every profile ($DSH_HOME/cordis.patch.yml).
package dsh

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/extfile"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is DeepSeek Harness.
type Agent struct{}

var _ agent.Agent = Agent{}

func (Agent) Name() string { return "dsh" }

// dir is DSH's home folder, found the way DSH finds it: a blank $DSH_HOME
// counts as unset.
func dir() string {
	if d := strings.TrimSpace(os.Getenv("DSH_HOME")); d != "" {
		return paths.Resolve(paths.Home(), d)
	}
	return filepath.Join(paths.Home(), ".dsh")
}

// ConfigPath is the patch file with the row that loads the plugin.
func (Agent) ConfigPath() string { return filepath.Join(dir(), "cordis.patch.yml") }

// PluginPath is the plugin that setup writes.
func (Agent) PluginPath() string { return filepath.Join(dir(), "ctxtra.mjs") }

func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath("dsh")
	return err == nil
}

// Setup writes the plugin, then the row that loads it, so DSH never loads a
// missing plugin; --remove goes the other way. A plugin file that ctxtra
// didn't write is left alone, and so is the patch file.
func (a Agent) Setup(o agent.Options, r *agent.Report) error {
	if o.Remove {
		if err := a.setupPatch(o, r); err != nil {
			return err
		}
		return extfile.Setup(a.PluginPath(), Plugin(o.Binary), o, r)
	}
	if err := extfile.Setup(a.PluginPath(), Plugin(o.Binary), o, r); err != nil {
		return err
	}
	return a.setupPatch(o, r)
}

func (a Agent) setupPatch(o agent.Options, r *agent.Report) error {
	path := a.ConfigPath()
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	block := ""
	if !o.Remove {
		block = patchBlock(a.PluginPath())
	}
	out, err := editPatch(string(old), block)
	if err != nil {
		return fmt.Errorf("%s: %w; left alone", path, err)
	}
	if out == string(old) {
		return nil
	}
	written, changed, err := fsutil.WriteAtomic(path, []byte(out), paths.BackupDir(), o.DryRun)
	if changed {
		r.Changes = append(r.Changes, written)
	}
	return err
}

// Status needs both the plugin and the row that loads it.
func (a Agent) Status() agent.Status {
	st := extfile.Status(a.PluginPath())
	b, err := os.ReadFile(a.ConfigPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return agent.Status{State: "unreadable: " + err.Error()}
	}
	loaded, ok := patchPlugin(string(b))
	plugin := st.State != "not set up"
	switch {
	case !ok && !plugin:
		return st
	case !ok:
		return agent.Status{State: "plugin written, but cordis.patch.yml doesn't load it; run ctxtra setup"}
	case loaded != a.PluginPath():
		return agent.Status{State: "cordis.patch.yml loads " + paths.Tilde(loaded) + "; run ctxtra setup"}
	case !plugin:
		return agent.Status{State: "cordis.patch.yml loads a missing plugin; run ctxtra setup"}
	}
	return st
}

// Answer is the files and the list of skills as one text, which the plugin
// adds to the agent's context. The plugin has no way to show the user a
// message, so there is no summary.
func (Agent) Answer(_ string, res *resolve.Result) any {
	return map[string]any{"text": res.Render()}
}

// Delivery: DSH gets 1,200,000 characters in full (measured with DSH 0.2.1-rc.2).
func (Agent) Delivery() agent.Delivery {
	return agent.Delivery{Title: "DSH", Capacity: 1_000_000}
}

// Plugin is the plugin that runs bin.
func Plugin(bin string) string { return extfile.Content(extfile.TypeScript, bin, pluginBody) }

// pluginBody runs the binary asynchronously, like Pi's extension, with a 10
// second timeout and room for the largest output ctxtra can produce.
//
// DSH emits agent/created for every new agent, subagents included, and
// waits for its listeners before the agent's first model request. Injected
// context goes into the session's history, so a resumed session already has
// it. The plugin is plain JavaScript and imports only Node's modules: it is
// loaded from outside any DSH profile, where DSH's own packages don't
// resolve.
const pluginBody = `// Written by ctxtra setup and loaded by its row in cordis.patch.yml. Adds the
// instruction files and skills that ctxtra binds to the session's folder.
// ` + "`ctxtra setup --remove`" + ` removes both.
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { homedir } from "node:os";
import { join } from "node:path";

const bin = %s;

function run(ev, cwd, signal) {
	return new Promise((resolve) => {
		try {
			const child = execFile(bin, ["hook", "dsh", ev], { encoding: "utf8", timeout: 10000, maxBuffer: 64 * 1024 * 1024, signal },
				(err, stdout) => {
					if (err) return resolve({});
					try {
						resolve(JSON.parse(stdout) ?? {});
					} catch {
						resolve({});
					}
				});
			child.stdin?.on("error", () => {});
			child.stdin?.end(JSON.stringify({ cwd }));
		} catch {
			resolve({});
		}
	});
}

// The session's folder: the workingDirectory service where DSH has one
// (0.2.1 and later), else the folder the session started in.
async function folder(ctx, agent, signal) {
	try {
		const wd = ctx.get("workingDirectory");
		if (wd) return await wd.ensure(agent, signal);
	} catch {}
	return agent.session?.header?.cwd ?? process.cwd();
}

export const name = "ctxtra";

// The text for each new agent's first step.
const pending = new WeakMap();

// The listeners never throw: DSH fails the agent's creation, or the step,
// when they do. Without an answer from ctxtra (no binary, timeout) the
// plugin adds nothing.
export function apply(ctx) {
	ctx.on("agent/created", async ({ agent, source, signal }) => {
		if (source === "resume") return;
		try {
			const ev = agent.session?.header?.parentSession ? "subagent" : source;
			const { text } = await run(ev, await folder(ctx, agent, signal), signal);
			if (text) pending.set(agent, { id: randomUUID(), role: "user", content: [{ type: "text", text }], source: { kind: "ctxtra" } });
		} catch {}
	});
	// Added at the end of the first step's messages, after the repository's
	// AGENTS.md, which DSH adds in this same waterfall. A step without
	// messages of its own keeps the text for the next one.
	ctx.on("agent/pre-step", async ({ agent }, next) => {
		const decision = await next();
		try {
			const message = pending.get(agent);
			if (!message || decision?.kind !== "enter" || !Array.isArray(decision.messages) || decision.messages.length === 0) return decision;
			pending.delete(agent);
			return { ...decision, messages: [...decision.messages, message] };
		} catch {
			return decision;
		}
	});
}
`
