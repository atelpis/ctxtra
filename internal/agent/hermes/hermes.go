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
	"sync"

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
	_ agent.OldSlots = Agent{}
)

// pluginName is the plugin's name in plugin.yaml and in plugins.enabled.
const pluginName = "ctxtra"

// pluginVersion is sent by the plugin as ctxtra_plugin in the hook input,
// and is its protocol line. Version 2 was the first with a callback per
// part; version 3 also sends ctxtra_slots. Plugins of older versions send
// none.
const pluginVersion = 3

// firstSlotted is the first plugin version with a callback per part.
const firstSlotted = 2

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
		if err := extfile.Owned(p); err != nil {
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
	doc, _ := readConfig()
	if on, _ := enabled(doc); !on {
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

// Status adds where the plugin is enabled.
func (a Agent) Status(bin string, known bool) agent.Status {
	s := extfile.Status(a.ConfigPath(), a.Check(bin, known))
	doc, _ := readConfig()
	if on, _ := enabled(doc); on && s.State != "not set up" {
		s.Details = append(s.Details, "plugin enabled in "+paths.Tilde(configPath()))
	}
	return s
}

// Check adds plugin.yaml, and whether the plugin is enabled: a plugin that
// isn't is set up, but Hermes doesn't load it. A plugin of an older version
// that gets as much as this version's is fine.
func (a Agent) Check(bin string, known bool) []string {
	config := sync.OnceValues(readConfig)
	notices, ours := extfile.CheckSetup(extfile.Ext{Path: a.ConfigPath(), Agent: a.Name(), Noun: "plugin",
		Legacy: func(v int) (agent.Delivery, bool) {
			doc, _ := config()
			return legacy(v, doc)
		}}, bin, known)
	if !ours {
		return notices
	}
	switch b, err := fsutil.ReadBounded(manifestPath(), agent.MaxConfig); {
	case errors.Is(err, os.ErrNotExist):
		notices = append(notices, fmt.Sprintf("%s is missing; %s", paths.Tilde(manifestPath()), agent.Repair(a.Name())))
	case err != nil:
		notices = append(notices, agent.UnreadableNotice(manifestPath(), err))
	case !strings.HasPrefix(string(b), extfile.Python.Mark()):
		notices = append(notices, fmt.Sprintf("%s isn't ctxtra's: delete it, then %s", paths.Tilde(manifestPath()), agent.Repair(a.Name())))
	}
	doc, err := config()
	on := false
	if err == nil {
		on, err = enabled(doc)
	}
	switch {
	case err != nil:
		notices = append(notices, fmt.Sprintf("can't tell whether the ctxtra plugin is enabled: %v", err))
	case !on:
		notices = append(notices, "the ctxtra plugin is installed but not enabled; run hermes plugins enable "+pluginName)
	}
	return notices
}

// enabled reports whether config.yaml, doc, lists the plugin in
// plugins.enabled and not in plugins.disabled, which wins in Hermes.
func enabled(doc *yaml.Node) (bool, error) {
	if doc == nil {
		return false, nil
	}
	var cfg struct {
		Plugins struct {
			Enabled  []string `yaml:"enabled"`
			Disabled []string `yaml:"disabled"`
		} `yaml:"plugins"`
	}
	if err := doc.Decode(&cfg); err != nil {
		return false, fmt.Errorf("%s: %w", paths.Tilde(configPath()), err)
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
	doc, _ := readConfig()
	size := min(partChars, spillChars(doc))
	return agent.Delivery{Title: "Hermes", Capacity: min(1_000_000, slots*size), Chunk: size, Slots: slots}
}

// OldSlots: a plugin of version 2, which doesn't send ctxtra_slots,
// registers 125 callbacks.
func (Agent) OldSlots() int { return 125 }

// Legacy: a plugin from before version 2 adds the text with one callback,
// which gets as much as Hermes doesn't spill.
func (Agent) Legacy(_ string, in agent.Input) (agent.Delivery, bool) {
	doc, _ := readConfig()
	return legacy(in.Plugin, doc)
}

func legacy(plugin int, doc *yaml.Node) (agent.Delivery, bool) {
	return agent.Delivery{Title: "Hermes", Capacity: min(partChars, spillChars(doc))}, plugin < firstSlotted
}

func configPath() string { return filepath.Join(dir(), "config.yaml") }

// readConfig parses config.yaml, for spillChars and enabled; nil when there
// is none.
func readConfig() (*yaml.Node, error) {
	b, err := fsutil.ReadBounded(configPath(), 4<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var doc yaml.Node
	if err == nil {
		err = yaml.Unmarshal(b, &doc)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", paths.Tilde(configPath()), err)
	}
	return &doc, nil
}

// spillChars is hooks.output_spill.max_chars in config.yaml, doc, read the
// way Hermes reads it: a whole number of at least 1, else 10,000. With
// spilling turned off, it is the largest int.
func spillChars(doc *yaml.Node) int {
	const fallback = 10_000
	if doc == nil {
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
	if doc.Decode(&cfg) != nil {
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
func Plugin(bin string) string {
	return extfile.Content(extfile.Python, bin, pluginVersion, pluginBody)
}

var manifest = extfile.Python.Mark() + `
# Written by ctxtra setup. Safe to delete; ` + "`ctxtra setup --remove`" + ` removes it.
name: ` + pluginName + `
version: "1"
description: Adds the instruction files and skills that ctxtra binds to the session's folder.
provides_hooks: [pre_llm_call]
`

// pluginBody runs the binary on the first turn of each session, subagents'
// sessions included, with a 10 second timeout, and reads at most 64 MiB of
// its output, like the other plugins. The binary runs in a session of its
// own, so the timeout also ends what it started, and the timeout covers
// writing its input too. Hermes adds the text to that
// turn's user message and sends it again with that message on every later
// turn, also after a resume. ctxtra logs a subagent's session as a
// "subagent" event, other sessions as "pre_llm_call". Hermes runs the
// callbacks one after another, in the order they were registered: the first
// runs the binary, and each callback adds one part.
var pluginBody = `# Written by ctxtra setup. Adds the instruction files and skills that
# ctxtra binds to the session's folder. Safe to delete; ` + "`ctxtra setup --remove`" + ` removes it.
import json
import os
import select
import signal
import subprocess
import time

BIN = %s

# Hermes cuts a callback's text above hooks.output_spill.max_chars, so ctxtra
# gives the text in parts, and callback K adds part K. A session's parts are
# kept from its first callback to its last.
SLOTS = ` + strconv.Itoa(slots) + `
TIMEOUT = 10
MAX_OUTPUT = 64 * 1024 * 1024
_parts = {}


def _cwd():
    # Every session runs in one gateway, whose own folder is another one.
    # When Hermes can't tell the session's folder, it raises, and the
    # plugin adds nothing rather than files bound to the wrong folder.
    from agent.runtime_cwd import resolve_agent_cwd

    return str(resolve_agent_cwd())


def _run(event):
    try:
        data = json.dumps(
            {"cwd": _cwd(), "ctxtra_plugin": ` + strconv.Itoa(pluginVersion) + `, "ctxtra_slots": SLOTS}, ensure_ascii=False
        ).encode("utf-8")
        proc = subprocess.Popen(
            [BIN, "hook", "hermes", event],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
    except Exception:
        return []
    deadline = time.monotonic() + TIMEOUT
    try:
        # A binary that doesn't read its input, or a process that it started
        # and that keeps stdout open, can block a write or a read: the
        # deadline holds for each.
        in_fd, out_fd = proc.stdin.fileno(), proc.stdout.fileno()
        os.set_blocking(in_fd, False)
        sent = 0
        out = bytearray()
        while True:
            left = deadline - time.monotonic()
            if left <= 0:
                return []
            writing = [] if proc.stdin.closed else [in_fd]
            readable, writable, _ = select.select([out_fd], writing, [], left)
            if not readable and not writable:
                return []
            if writable:
                try:
                    sent += os.write(in_fd, data[sent:])
                except BlockingIOError:
                    pass
                except BrokenPipeError:
                    sent = len(data)
                if sent >= len(data):
                    proc.stdin.close()
            if readable:
                b = os.read(out_fd, 1 << 16)
                if not b:
                    break
                out += b
                if len(out) > MAX_OUTPUT:
                    return []
        if proc.wait(timeout=max(deadline - time.monotonic(), 0.001)) != 0:
            return []
        parts = json.loads(bytes(out)).get("chunks")
    except Exception:
        return []
    finally:
        try:
            proc.stdin.close()
        except Exception:
            pass
        # Before the binary is reaped, its process group can't be another's.
        if proc.returncode is None:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except Exception:
                pass
            proc.wait()
        proc.stdout.close()
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
