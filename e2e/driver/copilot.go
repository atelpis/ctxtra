package driver

import (
	"os/exec"
	"path/filepath"
)

// Copilot is GitHub Copilot CLI, run with -p. Model requests go to the mock
// as a custom provider (BYOK, OpenAI Chat Completions); offline mode skips
// GitHub login, telemetry and updates.
type Copilot struct{}

func (Copilot) Name() string       { return "copilot" }
func (Copilot) Executable() string { return "copilot" }
func (Copilot) Caps() []Cap        { return []Cap{Subagent, Resume} }

func (Copilot) Command(bin, home, dir, mockURL, prompt string, o Options, env []string) (*exec.Cmd, error) {
	// --allow-all-paths: the scripted reads are outside the project folder,
	// which Copilot would otherwise ask about. --allow-all-tools, not
	// COPILOT_ALLOW_ALL=true, which would also trust the folder: user hooks
	// must run without that.
	cmd := exec.Command(bin, "-p", prompt, "--allow-all-tools", "--allow-all-paths", "--no-auto-update",
		"--output-format", "json", "--log-dir", filepath.Join(home, "copilot-logs"), "--log-level", "debug")
	if o.Resume {
		cmd.Args = append(cmd.Args, "--continue")
	}
	cmd.Dir = dir
	cmd.Env = append(env,
		"COPILOT_PROVIDER_BASE_URL="+mockURL+"/v1",
		"COPILOT_PROVIDER_API_KEY=mock",
		"COPILOT_MODEL=mock-model",
		"COPILOT_OFFLINE=true",
		"NO_COLOR=1")
	return cmd, nil
}

func (Copilot) UserMessages(string) []string { return nil }
