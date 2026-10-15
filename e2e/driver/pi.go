package driver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// Pi is Pi, run with -p. Model requests go to a custom provider in
// models.json (OpenAI Chat Completions). Pi has no built-in subagents.
type Pi struct{}

func (Pi) Name() string       { return "pi" }
func (Pi) Executable() string { return "pi" }
func (Pi) Caps() []Cap        { return nil }

func (Pi) Command(bin, home, dir, mockURL, prompt string, _ Options, env []string) (*exec.Cmd, error) {
	agentDir := filepath.Join(home, ".pi", "agent")
	models, _ := json.Marshal(map[string]any{"providers": map[string]any{"mock": map[string]any{
		"baseUrl": mockURL + "/v1", "api": "openai-completions", "apiKey": "mock",
		"models": []any{map[string]any{"id": "mock-model"}}}}})
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "-p", "--no-session", "--model", "mock/mock-model", prompt)
	cmd.Dir = dir
	cmd.Env = append(env, "PI_OFFLINE=1", "PI_TELEMETRY=0")
	return cmd, nil
}

func (Pi) UserMessages(string) []string { return nil }
