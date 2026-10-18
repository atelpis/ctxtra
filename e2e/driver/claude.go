package driver

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
)

// Claude is Claude Code, run with -p. Model requests go to the mock through
// ANTHROPIC_BASE_URL with a dummy API key.
type Claude struct{}

func (Claude) Name() string       { return "claude" }
func (Claude) Executable() string { return "claude" }
func (Claude) Caps() []Cap        { return []Cap{Subagent, UserMessage} }

func (Claude) Command(bin, home, dir, mockURL, prompt string, _ Options, env []string) (*exec.Cmd, error) {
	cmd := exec.Command(bin, "-p", prompt,
		"--output-format", "stream-json", "--verbose", "--include-hook-events",
		"--no-session-persistence",
		// Not auto mode (the default), whose safety classifier the mock
		// can't answer; allow only the tools the mock scripts use.
		"--permission-mode", "default", "--allowedTools", "Read,Agent",
		"--debug-file", filepath.Join(home, "claude-debug.log"))
	cmd.Dir = dir
	cmd.Env = append(env,
		"ANTHROPIC_BASE_URL="+mockURL,
		"ANTHROPIC_API_KEY=sk-e2e-mock",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1")
	return cmd, nil
}

// UserMessages reads hook responses from the stream-json output: Claude
// shows a hook's systemMessage to the user.
func (Claude) UserMessages(_, stdout string) []string {
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		var ev struct {
			Type, Subtype string
			Output        string
			Stdout        string
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "system" || ev.Subtype != "hook_response" {
			continue
		}
		text := ev.Stdout // output also has stderr
		if text == "" {
			text = ev.Output
		}
		var ans struct{ SystemMessage string }
		if json.Unmarshal([]byte(strings.TrimSpace(text)), &ans) == nil && ans.SystemMessage != "" {
			out = append(out, ans.SystemMessage)
		}
	}
	return out
}
