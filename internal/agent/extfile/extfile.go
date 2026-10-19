// Package extfile manages the extension files that ctxtra writes for agents
// without command hooks: a TypeScript module (Pi, OpenCode) or a Python
// plugin (Hermes) that runs the binary. The first line marks the file as
// ctxtra's; the second names the binary, for Status; the third the version
// of ctxtra's plugin protocol that the file speaks, which it also sends as
// ctxtra_plugin.
package extfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/hookjson"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Syntax is the language of an extension file.
type Syntax struct {
	comment string // starts a line comment
	home    string // an expression for a path under the home folder; %s is the relative path as a JSON string
}

var (
	// TypeScript bodies import homedir and join.
	TypeScript = Syntax{"//", "join(homedir(), %s)"}
	// Python bodies import os.
	Python = Syntax{"#", `os.path.join(os.path.expanduser("~"), %s)`}

	syntaxes = []Syntax{TypeScript, Python}
)

// Mark is the first line of every file that ctxtra writes in this syntax.
func (s Syntax) Mark() string { return s.comment + " @ctxtra-managed" }

func (s Syntax) binaryMark() string { return s.comment + " binary: " }

func (s Syntax) protocolMark() string { return s.comment + " protocol: " }

// Content is the extension for bin, of version protocol. body has one %s,
// where the binary path goes as an expression: a JSON string, which is also
// a valid TypeScript and Python string, or a path under the home folder, so
// the file works for another user name.
func Content(s Syntax, bin string, protocol int, body string) string {
	js, _ := json.Marshal(bin)
	expr := string(js)
	if t := paths.Tilde(bin); strings.HasPrefix(t, "~/") {
		rel, _ := json.Marshal(t[2:])
		expr = fmt.Sprintf(s.home, rel)
	}
	return s.Mark() + "\n" + s.binaryMark() + agent.ShellPath(bin) + "\n" + s.protocolMark() + strconv.Itoa(protocol) + "\n" +
		fmt.Sprintf(body, expr)
}

// maxFile bounds what Protocol reads.
const maxFile = 1 << 20

// rePluginField is the version that a Python plugin from before the
// protocol line sends (Hermes).
var rePluginField = regexp.MustCompile(`"ctxtra_plugin": (\d+)`)

// Protocol returns the protocol version of the extension at path, and
// whether there is one of ctxtra's there. A file without the protocol line
// is version 0, from before files said, or, for a Python plugin, the
// ctxtra_plugin that it sends.
func Protocol(path string) (int, bool) {
	b, err := fsutil.ReadBounded(path, maxFile)
	if err != nil || !managed(b) {
		return 0, false
	}
	return protocol(b)
}

func protocol(b []byte) (int, bool) {
	for line := range strings.Lines(string(b)) {
		for _, s := range syntaxes {
			if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), s.protocolMark()); ok {
				n, err := strconv.Atoi(v)
				return n, err == nil && n >= 0
			}
		}
	}
	if bytes.HasPrefix(b, []byte(Python.Mark())) {
		if m := rePluginField.FindSubmatch(b); m != nil {
			n, err := strconv.Atoi(string(m[1]))
			return n, err == nil
		}
	}
	return 0, true
}

// Owned reports an error when a file exists at path that ctxtra didn't
// write.
func Owned(path string) error {
	_, _, err := read(path)
	return err
}

// read returns the file at path and whether it exists; an error for a file
// that ctxtra didn't write.
func read(path string) ([]byte, bool, error) {
	b, err := fsutil.ReadBounded(path, agent.MaxConfig)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !managed(b) {
		return nil, true, fmt.Errorf("%s exists and is not managed by ctxtra; left alone", path)
	}
	return b, true, nil
}

func managed(b []byte) bool {
	for _, s := range syntaxes {
		if bytes.HasPrefix(b, []byte(s.Mark())) {
			return true
		}
	}
	return false
}

// Setup writes content to path, or removes the file with o.Remove. A file
// that ctxtra didn't write is left alone.
func Setup(path, content string, o agent.Options, r *agent.Report) error {
	_, exists, err := read(path)
	if err != nil {
		return err
	}
	if !o.Remove {
		written, changed, err := fsutil.WriteAtomic(path, []byte(content), paths.BackupDir(), o.DryRun)
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

// Status is the setup state of the extension at path, with Check's
// notices.
func Status(path string, notices []string) agent.Status {
	b, err := fsutil.ReadBounded(path, agent.MaxConfig)
	if err != nil || !managed(b) {
		return agent.Status{State: "not set up", OK: len(notices) == 0, Details: notices}
	}
	bin, ok := binary(string(b))
	if !ok {
		return agent.Status{State: "set up by an unknown version", Details: notices}
	}
	s := hookjson.BinaryStatus("set up", []string{bin})
	s.OK = s.OK && len(notices) == 0
	s.Details = notices
	return s
}

// Ext is an agent's extension, for CheckSetup.
type Ext struct {
	Path, Agent string
	Noun        string // "plugin" or "extension", in notices
	// Legacy is what an extension of a protocol version gets, when that is
	// less than a new one gets; nil when none does.
	Legacy func(protocol int) (agent.Delivery, bool)
}

// CheckSetup is agent.Agent.Check for the extension e: the binary it runs,
// an older protocol version that gets less, and an extension that is gone
// although known. ours is whether ctxtra's extension is there.
func CheckSetup(e Ext, bin string, known bool) (notices []string, ours bool) {
	repair := agent.Repair(e.Agent)
	b, err := fsutil.ReadBounded(e.Path, agent.MaxConfig)
	switch {
	case errors.Is(err, os.ErrNotExist) || err == nil && !managed(b):
		if known {
			return []string{agent.GoneNotice("ctxtra's "+e.Noun+" is", e.Path, e.Agent, repair)}, false
		}
		return nil, false
	case err != nil:
		return []string{agent.UnreadableNotice(e.Path, err)}, false
	}
	if runs, ok := binary(string(b)); ok {
		notices = agent.BinaryNotices("the "+e.Noun+" runs", []string{runs}, bin, repair)
	} else {
		notices = append(notices, fmt.Sprintf("%s doesn't say which binary it runs; %s", paths.Tilde(e.Path), repair))
	}
	if v, ok := protocol(b); ok && e.Legacy != nil {
		if old, ok := e.Legacy(v); ok {
			notices = append(notices, agent.LegacyNotice(old, repair))
		}
	}
	return notices, true
}

// binary returns the binary in the comment that Content writes.
func binary(ext string) (string, bool) {
	for line := range strings.Lines(ext) {
		for _, s := range syntaxes {
			if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), s.binaryMark()); ok && v != "" {
				return agent.ExpandShellPath(v), true
			}
		}
	}
	return "", false
}
