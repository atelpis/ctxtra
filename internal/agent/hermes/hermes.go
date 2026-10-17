// Package hermes supports Hermes Agent. Its shell hooks get the folder of
// the gateway that runs every session, not the session's folder, so setup
// writes a small Python plugin that runs ctxtra instead. Hermes loads a user
// plugin only after the user enables it.
package hermes

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/extfile"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// Agent is Hermes Agent.
type Agent struct{}

var _ agent.Agent = Agent{}

// pluginName is the plugin's name in plugin.yaml and in plugins.enabled.
const pluginName = "ctxtra"

const enableNote = "hermes: the plugin runs only after you enable it: run `hermes plugins enable " + pluginName +
	"`. It applies from the next session."

func (Agent) Name() string { return "hermes" }

func dir() string {
	if d := os.Getenv("HERMES_HOME"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".hermes")
}

func pluginDir() string { return filepath.Join(dir(), "plugins", pluginName) }

func manifestPath() string { return filepath.Join(pluginDir(), "plugin.yaml") }

// ConfigPath is the plugin's code; setup also writes plugin.yaml next to it.
func (Agent) ConfigPath() string { return filepath.Join(pluginDir(), "__init__.py") }

func (Agent) Detect() bool {
	if st, err := os.Stat(dir()); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath("hermes")
	return err == nil
}

// Setup writes or removes the plugin's two files. Hermes skips a folder
// without plugin.yaml, so it is written last and removed first. Files that
// ctxtra didn't write are left alone, and then neither file changes.
func (a Agent) Setup(o agent.Options, r *agent.Report) error {
	for _, p := range []string{a.ConfigPath(), manifestPath()} {
		if err := extfile.Check(p); err != nil {
			return err
		}
	}
	if o.Remove {
		if err := extfile.Setup(manifestPath(), "", o, r); err != nil {
			return err
		}
		if err := extfile.Setup(a.ConfigPath(), "", o, r); err != nil || o.DryRun {
			return err
		}
		removeDir()
		return nil
	}
	if err := extfile.Setup(a.ConfigPath(), Plugin(o.Binary), o, r); err != nil {
		return err
	}
	if err := extfile.Setup(manifestPath(), manifest, o, r); err != nil {
		return err
	}
	if on, _ := enabled(); !on {
		r.Notes = append(r.Notes, enableNote)
	}
	return nil
}

// removeDir removes the plugin folder after its files, with the bytecode
// Python wrote for __init__.py. Anything else in the folder keeps it.
func removeDir() {
	cache := filepath.Join(pluginDir(), "__pycache__")
	if entries, err := os.ReadDir(cache); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "__init__.") && strings.HasSuffix(e.Name(), ".pyc") {
				os.Remove(filepath.Join(cache, e.Name()))
			}
		}
		os.Remove(cache)
	}
	os.Remove(pluginDir())
}

// Status adds whether plugin.yaml is in place and the plugin is enabled.
func (a Agent) Status() agent.Status {
	s := extfile.Status(a.ConfigPath())
	if s.State == "not set up" {
		return s
	}
	if b, err := os.ReadFile(manifestPath()); err != nil || !strings.HasPrefix(string(b), extfile.Python.Mark()) {
		s.OK = false
		s.Details = append(s.Details, paths.Tilde(manifestPath())+": MISSING (run ctxtra setup)")
	}
	cfg := paths.Tilde(filepath.Join(dir(), "config.yaml"))
	switch on, err := enabled(); {
	case err != nil:
		s.OK = false
		s.Details = append(s.Details, fmt.Sprintf("plugin state unknown: %v", err))
	case on:
		s.Details = append(s.Details, "plugin enabled in "+cfg)
	default:
		s.OK = false
		s.Details = append(s.Details, fmt.Sprintf("plugin NOT ENABLED in %s (run hermes plugins enable %s)", cfg, pluginName))
	}
	return s
}

// enabled reports whether config.yaml lists the plugin in plugins.enabled
// and not in plugins.disabled, which wins in Hermes.
func enabled() (bool, error) {
	path := filepath.Join(dir(), "config.yaml")
	b, err := fsutil.ReadBounded(path, 4<<20)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var cfg struct {
		Plugins struct {
			Enabled  []string `yaml:"enabled"`
			Disabled []string `yaml:"disabled"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return false, fmt.Errorf("%s: %w", paths.Tilde(path), err)
	}
	return slices.Contains(cfg.Plugins.Enabled, pluginName) && !slices.Contains(cfg.Plugins.Disabled, pluginName), nil
}

// Answer is the text for the first user message, files and the skill list
// like Claude and Codex: a plugin's skills are registered once per gateway,
// not per session. Hermes has no way to show the user a message from a
// plugin, so there is no summary.
func (Agent) Answer(_ string, res *resolve.Result) any {
	if text := res.Render(); text != "" {
		return map[string]any{"context": text}
	}
	return map[string]any{}
}

// PreviewLimit: Hermes writes larger hook context to a file and gives the
// model a preview (hooks.output_spill.max_chars).
func (Agent) PreviewLimit() int { return 10000 }

// Plugin is the plugin code that runs bin.
func Plugin(bin string) string { return extfile.Content(extfile.Python, bin, pluginBody) }

var manifest = extfile.Python.Mark() + `
# Written by ctxtra setup. Safe to delete; ` + "`ctxtra setup --remove`" + ` removes it.
name: ` + pluginName + `
version: "1"
description: Adds the instruction files and skills that ctxtra binds to the session's folder.
provides_hooks: [pre_llm_call]
`

// pluginBody runs the binary on the first turn of each session, subagents'
// sessions included, with a 10 second timeout. Hermes adds the text to that
// turn's user message and sends it again with that message on every later
// turn, also after a resume. ctxtra logs a subagent's session as a
// "subagent" event, other sessions as "pre_llm_call".
const pluginBody = `# Written by ctxtra setup. Adds the instruction files and skills that
# ctxtra binds to the session's folder. Safe to delete; ` + "`ctxtra setup --remove`" + ` removes it.
import json
import os
import subprocess

BIN = %s


def _cwd():
    # Every session runs in one gateway, whose own folder is another one.
    # When Hermes can't tell the session's folder, it raises, and the
    # plugin adds nothing rather than files bound to the wrong folder.
    from agent.runtime_cwd import resolve_agent_cwd

    return str(resolve_agent_cwd())


def _run(event):
    try:
        out = subprocess.run(
            [BIN, "hook", "hermes", event],
            input=json.dumps({"cwd": _cwd()}),
            capture_output=True,
            text=True,
            timeout=10,
        ).stdout
        context = json.loads(out).get("context")
    except Exception:
        return None
    return {"context": context} if isinstance(context, str) and context else None


def _pre_llm_call(is_first_turn=False, parent_session_id="", **kwargs):
    if not is_first_turn:
        return None
    return _run("subagent" if parent_session_id else "pre_llm_call")


def register(ctx):
    ctx.register_hook("pre_llm_call", _pre_llm_call)
`
