package driver

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Muse is Muse Code (Meta), run with `muse exec`. Model requests go to the
// mock through --base-url (Responses API), with a dummy API key.
type Muse struct{}

func (Muse) Name() string       { return "muse" }
func (Muse) Executable() string { return "muse" }
func (Muse) Caps() []Cap        { return []Cap{Subagent, UserMessage, Resume} }

func (Muse) Command(bin, home, dir, mockURL, prompt string, o Options, env []string) (*exec.Cmd, error) {
	// --trust-workspace: Muse skips the repo's AGENTS.md and subagents in an
	// untrusted folder. Without reminders, Muse starts no background agents
	// of its own, and without web tools nothing can leave the machine.
	args := []string{"exec", "--json", "--base-url", mockURL + "/v1", "--model", "mock-model",
		"--trust-workspace", "--disable-reminders", "--disable-web-tools"}
	if o.Resume {
		id, err := lastSession(home)
		if err != nil {
			return nil, err
		}
		args = append(args, "--session-id", id)
	}
	cmd := exec.Command(bin, append(args, prompt)...)
	cmd.Dir = dir
	// MUSE_NO_AUTO_UPDATE: the launcher script doesn't download a new
	// version in the background.
	cmd.Env = append(env, "META_API_KEY=mock", "MUSE_NO_AUTO_UPDATE=1")
	return cmd, nil
}

// lastSession is the ID of the newest session in the session logs: `muse
// exec` resumes only by ID.
func lastSession(home string) (string, error) {
	newest, id := time.Time{}, ""
	for _, p := range sessionLogs(home) {
		if st, err := os.Stat(p); err == nil && st.ModTime().After(newest) {
			newest, id = st.ModTime(), filepath.Base(filepath.Dir(p))
		}
	}
	if id == "" {
		return "", errors.New("muse: no session to resume")
	}
	return id, nil
}

func sessionLogs(home string) []string {
	logs, _ := filepath.Glob(filepath.Join(home, ".local", "share", "muse", "sessions", "*", "*", "*", "*", "session.jsonl"))
	return logs
}

// UserMessages reads hook runs from the session logs: `muse exec --json`
// doesn't print a hook's systemMessage, the session log has it.
func (Muse) UserMessages(home, _ string) []string {
	var out []string
	for _, p := range sessionLogs(home) {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 16<<20)
		for sc.Scan() {
			var rec struct {
				Payload struct {
					Event struct {
						Kind          string
						SystemMessage string `json:"system_message"`
					}
				}
			}
			if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Payload.Event.Kind == "hook_run_terminal" &&
				rec.Payload.Event.SystemMessage != "" {
				out = append(out, rec.Payload.Event.SystemMessage)
			}
		}
		f.Close()
	}
	return out
}
