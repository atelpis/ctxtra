// Package extfile manages the extension files that ctxtra writes for agents
// without command hooks (Pi, OpenCode): a TypeScript module that runs the
// binary. The first line marks the file as ctxtra's; the second names the
// binary, for Status.
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

const (
	mark       = "// @ctxtra-managed"
	binaryMark = "// binary: "
)

// Content is the extension for bin. body has one %s, where the binary path
// goes as a JavaScript expression: a JSON string, which is also a valid
// TypeScript string, or join(homedir(), ...) for a binary under the home
// folder, so the file works for another user name. body imports homedir
// and join.
func Content(bin, body string) string {
	js, _ := json.Marshal(bin)
	expr := string(js)
	if t := paths.Tilde(bin); strings.HasPrefix(t, "~/") {
		rel, _ := json.Marshal(t[2:])
		expr = fmt.Sprintf("join(homedir(), %s)", rel)
	}
	return mark + "\n" + binaryMark + agent.ShellPath(bin) + "\n" + fmt.Sprintf(body, expr)
}

// Setup writes content to path, or removes the file with o.Remove. A file
// that ctxtra didn't write is left alone.
func Setup(path, content string, o agent.Options, r *agent.Report) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	exists := err == nil
	if exists && !bytes.HasPrefix(old, []byte(mark)) {
		return fmt.Errorf("%s exists and is not managed by ctxtra; left alone", path)
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
	if err != nil || !bytes.HasPrefix(b, []byte(mark)) {
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
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), binaryMark); ok && v != "" {
			return agent.ExpandShellPath(v), true
		}
	}
	return "", false
}
