// Package pi supports Pi. Pi has no command hooks, so setup writes a small
// extension that runs ctxtra and adds its output as context files and
// native skills.
package pi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Pi.
type Agent struct{}

var _ agent.Agent = Agent{}

const (
	mark       = "// @ctxtra-managed"
	binaryMark = "// binary: "
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
	path := a.ConfigPath()
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	exists := err == nil
	if exists && !bytes.HasPrefix(old, []byte(mark)) {
		return fmt.Errorf("%s exists and is not managed by ctxtra; left alone", path)
	}
	if !o.Remove {
		written, changed, err := fsutil.WriteAtomic(path, []byte(Extension(o.Binary)), paths.BackupDir(), o.DryRun)
		if changed {
			r.Changes = append(r.Changes, written)
		}
		return err
	}
	if !exists {
		return nil
	}
	r.Changes = append(r.Changes, path)
	if o.DryRun {
		return nil
	}
	return os.Remove(path)
}

func (a Agent) Status() agent.Status {
	b, err := os.ReadFile(a.ConfigPath())
	if err != nil || !bytes.HasPrefix(b, []byte(mark)) {
		return agent.Status{State: "not set up", OK: true}
	}
	bin, ok := extensionBinary(string(b))
	if !ok {
		return agent.Status{State: "set up by an unknown version; run ctxtra setup"}
	}
	return hookjson.BinaryStatus("set up", []string{bin})
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

func (Agent) PreviewLimit() int { return 0 }

// Extension is the extension that runs bin. The path is in a comment for
// Status, and in code as a JSON string, which is also a valid TypeScript
// string.
func Extension(bin string) string {
	js, _ := json.Marshal(bin)
	expr := string(js)
	if t := paths.Tilde(bin); strings.HasPrefix(t, "~/") {
		rel, _ := json.Marshal(t[2:])
		expr = fmt.Sprintf("join(homedir(), %s)", rel)
	}
	return mark + "\n" + binaryMark + agent.ShellPath(bin) + "\n" + fmt.Sprintf(extensionBody, expr)
}

// extensionBinary returns the binary in the comment that Extension writes.
func extensionBinary(ext string) (string, bool) {
	for line := range strings.Lines(ext) {
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), binaryMark); ok && v != "" {
			return agent.ExpandShellPath(v), true
		}
	}
	return "", false
}

// extensionBody runs the binary asynchronously, so Pi's event loop never
// waits on it, with a 10 second timeout and room for the largest output
// ctxtra can produce (4 MiB of files, JSON-escaped).
const extensionBody = `// Written by ctxtra setup. Adds the instruction files and skills that
// ctxtra binds to the current folder. Safe to delete; ` + "`ctxtra setup --remove`" + ` removes it.
import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";

const bin = %s;

function run(ev: string, cwd: string): Promise<any> {
	return new Promise((resolve) => {
		try {
			const child = execFile(bin, ["hook", "pi", ev], { encoding: "utf8", timeout: 10000, maxBuffer: 64 * 1024 * 1024 },
				(err, stdout) => {
					if (err) return resolve({});
					try {
						resolve(JSON.parse(stdout));
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

export default function ctxtra(pi: any) {
	// Tells the user once per session what ctxtra added.
	pi.on("session_start", async (_event: any, ctx: any) => {
		const message = (await run("session_start", ctx.cwd)).message;
		if (message && ctx.hasUI) ctx.ui.notify(message, "info");
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
