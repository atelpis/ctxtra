// Package hermes supports Hermes Agent. Its shell hooks get the folder of
// the gateway that runs every session, not the session's folder, so setup
// writes a small Python plugin that runs ctxtra instead. Hermes loads a user
// plugin only after the user enables it.
package hermes

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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

var (
	_ agent.Agent    = Agent{}
	_ agent.Upgrader = Agent{}
)

// pluginName is the plugin's name in plugin.yaml and in plugins.enabled.
const pluginName = "ctxtra"

// pluginVersion is sent by the plugin as ctxtra_plugin in the hook input.
// Plugins of older versions send none.
const pluginVersion = 2

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

// Status adds whether plugin.yaml is in place, the plugin is this
// version's, and it is enabled.
func (a Agent) Status() agent.Status {
	s := extfile.Status(a.ConfigPath())
	if s.State == "not set up" {
		return s
	}
	if b, err := os.ReadFile(a.ConfigPath()); err == nil && !isPlugin(string(b)) {
		s.OK = false
		s.Details = append(s.Details, paths.Tilde(a.ConfigPath())+": written by another version of ctxtra (run ctxtra setup)")
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
// not per session. The plugin's callback K adds part K of chunks. context
// is the whole text, for a plugin written by an older version. Hermes has no
// way to show the user a message from a plugin, so there is no summary.
func (a Agent) Answer(_ string, res *resolve.Result) any {
	if text := res.Render(); text != "" {
		return map[string]any{"chunks": a.Delivery().Chunks(res), "context": text}
	}
	return map[string]any{}
}

const (
	// slots is how many pre_llm_call callbacks the plugin registers: enough
	// for 1,000,000 characters in parts of 9,000, with room for the labels.
	slots = 125
	// partChars is the largest part, in characters.
	partChars = 9000
)

// Delivery: Hermes writes a callback's text over hooks.output_spill.max_chars
// (10,000 by default) to a file and gives the model 500 characters of its
// start and end, but each callback's text on its own and in order (measured
// with Hermes v2026.9.24). A lower max_chars makes the parts smaller.
func (Agent) Delivery() agent.Delivery {
	size := min(partChars, spillChars())
	return agent.Delivery{Title: "Hermes", Capacity: min(1_000_000, slots*size), Chunk: size, Slots: slots}
}

// Legacy: a plugin of an older version adds the text with one callback,
// which gets as much as Hermes doesn't spill.
func (Agent) Legacy(_ string, in agent.Input) (agent.Delivery, bool) {
	return agent.Delivery{Title: "Hermes", Capacity: min(partChars, spillChars())}, in.Plugin < pluginVersion
}

// spillChars is hooks.output_spill.max_chars in config.yaml, read the way
// Hermes reads it: a whole number of at least 1, else 10,000. With spilling
// turned off, it is the largest int.
func spillChars() int {
	const fallback = 10_000
	b, err := fsutil.ReadBounded(filepath.Join(dir(), "config.yaml"), 4<<20)
	if err != nil {
		return fallback
	}
	var cfg struct {
		Hooks struct {
			Spill struct {
				Enabled  *bool `yaml:"enabled"`
				MaxChars any   `yaml:"max_chars"`
			} `yaml:"output_spill"`
		} `yaml:"hooks"`
	}
	if yaml.Unmarshal(b, &cfg) != nil {
		return fallback
	}
	sp := cfg.Hooks.Spill
	if sp.Enabled != nil && !*sp.Enabled {
		return math.MaxInt
	}
	n := 0
	switch v := sp.MaxChars.(type) {
	case int:
		n = v
	case float64:
		n = int(v)
	case string:
		n, _ = strconv.Atoi(strings.TrimSpace(v))
	}
	if n < 1 {
		return fallback
	}
	return n
}

// Plugin is the plugin code that runs bin.
func Plugin(bin string) string { return extfile.Content(extfile.Python, bin, pluginBody) }

// isPlugin reports whether code is Plugin for the binary it names.
func isPlugin(code string) bool {
	for line := range strings.Lines(code) {
		if bin, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), "# binary: "); ok {
			return code == Plugin(agent.ExpandShellPath(bin))
		}
	}
	return false
}

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
// "subagent" event, other sessions as "pre_llm_call". Hermes runs the
// callbacks one after another, in the order they were registered: the first
// runs the binary, and each callback adds one part.
var pluginBody = `# Written by ctxtra setup. Adds the instruction files and skills that
# ctxtra binds to the session's folder. Safe to delete; ` + "`ctxtra setup --remove`" + ` removes it.
import json
import os
import subprocess

BIN = %s

# Hermes cuts a callback's text above hooks.output_spill.max_chars, so ctxtra
# gives the text in parts, and callback K adds part K. A session's parts are
# kept from its first callback to its last.
SLOTS = ` + strconv.Itoa(slots) + `
_parts = {}


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
            input=json.dumps({"cwd": _cwd(), "ctxtra_plugin": ` + strconv.Itoa(pluginVersion) + `}),
            capture_output=True,
            text=True,
            timeout=10,
        ).stdout
        parts = json.loads(out).get("chunks")
    except Exception:
        return []
    if not isinstance(parts, list) or not all(isinstance(p, str) for p in parts):
        return []
    return parts


def _callback(k):
    def _pre_llm_call(is_first_turn=False, parent_session_id="", session_id="", **kwargs):
        if not is_first_turn:
            return None
        if k == 0:
            _parts[session_id] = _run("subagent" if parent_session_id else "pre_llm_call")
        parts = _parts.pop(session_id, []) if k == SLOTS - 1 else _parts.get(session_id, [])
        return {"context": parts[k]} if k < len(parts) and parts[k] else None

    return _pre_llm_call


def register(ctx):
    for k in range(SLOTS):
        ctx.register_hook("pre_llm_call", _callback(k))
`
