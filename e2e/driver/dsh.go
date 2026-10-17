package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// DSH is DeepSeek Harness, run with its headless profile. Model requests go
// to a custom provider (OpenAI Chat Completions) in a --patch overlay, so
// the home patch file stays as ctxtra setup left it.
type DSH struct{}

func (DSH) Name() string       { return "dsh" }
func (DSH) Executable() string { return "dsh" }
func (DSH) Caps() []Cap        { return []Cap{Subagent} }

func (DSH) Command(bin, home, dir, mockURL, prompt string, _ Options, env []string) (*exec.Cmd, error) {
	patch := fmt.Sprintf(`- id: llm-pi-ai
  config:
    providers:
      mock:
        apiKeyEnv: E2E_MOCK_KEY
        api: openai-completions
        baseURL: %q
        compat:
          supportsDeveloperRole: false
          maxTokensField: max_tokens
        models:
          - id: mock-model
- id: agent-default-model
  config:
    provider: mock
    model: mock-model
`, mockURL+"/v1")
	path := filepath.Join(home, "e2e", "dsh-mock.patch.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(patch), 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "--profile", "headless", "--patch", path, prompt)
	cmd.Dir = dir
	cmd.Env = append(env, "E2E_MOCK_KEY=mock", "DSH_TELEMETRY_MODE=DISABLED")
	return cmd, nil
}

func (DSH) UserMessages(_, _ string) []string { return nil }
