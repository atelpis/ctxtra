// Package extfile manages the extension files that ctxtra writes for agents
// without command hooks: a TypeScript module (Pi, OpenCode) or a Python
// plugin (Hermes) that runs the binary. The first line marks the file as
// ctxtra's; the second names the binary, for Status.
package extfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// Content is the extension for bin. body has one %s, where the binary path
// goes as an expression: a JSON string, which is also a valid TypeScript and
// Python string, or a path under the home folder, so the file works for
// another user name.
func Content(s Syntax, bin, body string) string {
	js, _ := json.Marshal(bin)
	expr := string(js)
	if t := paths.Tilde(bin); strings.HasPrefix(t, "~/") {
		rel, _ := json.Marshal(t[2:])
		expr = fmt.Sprintf(s.home, rel)
	}
	return s.Mark() + "\n" + s.binaryMark() + agent.ShellPath(bin) + "\n" + fmt.Sprintf(body, expr)
}

// Check reports an error when a file exists at path that ctxtra didn't
// write.
func Check(path string) error {
	_, _, err := read(path)
	return err
}

// read returns the file at path and whether it exists; an error for a file
// that ctxtra didn't write.
func read(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
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

// Status is the setup state of the extension at path.
func Status(path string) agent.Status {
	b, err := os.ReadFile(path)
	if err != nil || !managed(b) {
		return agent.Status{State: "not set up", OK: true}
	}
	bin, ok := binary(string(b))
	if !ok {
		return agent.Status{State: "set up by an unknown version; run ctxtra setup"}
	}
	return hookjson.BinaryStatus("set up", []string{bin})
}

// binary returns the binary in the comment that Content writes.
func binary(ext string) (string, bool) {
	for line := range strings.Lines(ext) {
		for _, s := range syntaxes {
			if v, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), s.binaryMark()); ok && v != "" {
				return agent.ExpandShellPath(v), true
			}
		}
	}
	return "", false
}
