package driver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// Antigravity is the Antigravity CLI (agy), run with -p. Model requests go
// to the mock through GOOGLE_GEMINI_BASE_URL with a dummy Gemini API key
// (Gemini API). A subagent is a session of its own, so ctxtra's
// SessionStart hook runs for it too.
type Antigravity struct{}

func (Antigravity) Name() string       { return "antigravity" }
func (Antigravity) Executable() string { return "agy" }
func (Antigravity) Caps() []Cap        { return []Cap{Subagent} }

func (Antigravity) Command(bin, home, dir, mockURL, prompt string, _ Options, env []string) (*exec.Cmd, error) {
	// The API key mode needs modelProvider; without it agy asks for a
	// sign-in. Headless runs can't ask for permissions, so reads outside
	// the workspace (skills, the scripted reads) are allowed in home.
	if err := agySettings(home); err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "-p", prompt, "--output-format", "json")
	cmd.Dir = dir
	cmd.Env = append(env, "GEMINI_API_KEY=mock", "GOOGLE_GEMINI_BASE_URL="+mockURL,
		"AGY_CLI_DISABLE_AUTO_UPDATE=true")
	return cmd, nil
}

func (Antigravity) UserMessages(string, string) []string { return nil }

// agySettings writes the CLI's settings: the Gemini API with a key, and
// reads in home allowed.
func agySettings(home string) error {
	settings, _ := json.Marshal(map[string]any{"modelProvider": "gemini", "enableTelemetry": false,
		"permissions": map[string]any{"allow": []string{"read_file(" + home + ")"}}})
	cfgDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfgDir, "settings.json"), settings, 0o644)
}
