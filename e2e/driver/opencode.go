package driver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// OpenCode is OpenCode 2, run with `opencode run --standalone`: its own
// server, not the shared background service. Model requests go to a custom
// provider in the global config (OpenAI Chat Completions).
type OpenCode struct{}

func (OpenCode) Name() string       { return "opencode" }
func (OpenCode) Executable() string { return "opencode" }
func (OpenCode) Caps() []Cap        { return []Cap{Subagent, SkillTool} }

func (OpenCode) Command(bin, home, dir, mockURL, prompt string, _ Options, env []string) (*exec.Cmd, error) {
	cfg, _ := json.Marshal(map[string]any{"update": "disable", "providers": map[string]any{"mock": map[string]any{
		"name": "mock", "env": []string{"E2E_MOCK_KEY"}, "package": "@opencode/ai/providers/openai-compatible",
		"settings": map[string]any{"baseURL": mockURL + "/v1"},
		"models":   map[string]any{"mock-model": map[string]any{"name": "mock-model"}}}}})
	cfgDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "opencode.json"), cfg, 0o644); err != nil {
		return nil, err
	}
	// --auto: the scripted reads are outside the project folder, which
	// OpenCode would otherwise ask about.
	cmd := exec.Command(bin, "run", "--standalone", "--auto", "--model", "mock/mock-model", prompt)
	cmd.Dir = dir
	// No model catalog download, update check or file watcher: the only
	// network calls left are to the mock.
	cmd.Env = append(env, "E2E_MOCK_KEY=mock", "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_FILEWATCHER=1")
	return cmd, nil
}

func (OpenCode) UserMessages(_, _ string) []string { return nil }
