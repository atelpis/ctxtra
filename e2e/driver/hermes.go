package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Hermes is Hermes Agent, run with `hermes chat -Q -q`. Model requests go to
// a custom provider in config.yaml (OpenAI Chat Completions). The config
// also enables every plugin in ~/.hermes/plugins, ctxtra's included: the
// step `hermes plugins enable NAME` does for the user.
type Hermes struct{}

func (Hermes) Name() string       { return "hermes" }
func (Hermes) Executable() string { return "hermes" }
func (Hermes) Caps() []Cap        { return []Cap{Subagent} }

func (Hermes) Command(bin, home, dir, mockURL, prompt string, _ Options, env []string) (*exec.Cmd, error) {
	cfgDir := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return nil, err
	}
	var plugins []string
	entries, _ := os.ReadDir(filepath.Join(cfgDir, "plugins"))
	for _, e := range entries {
		if e.IsDir() {
			plugins = append(plugins, e.Name())
		}
	}
	cfg := strings.NewReplacer("MOCK", mockURL, "PLUGINS", strings.Join(plugins, ", ")).Replace(`model:
  provider: mock
  default: mock-model
providers:
  mock:
    api: MOCK/v1
    api_key: mock
    discover_models: false
    models: [mock-model]
plugins:
  enabled: [PLUGINS]
`)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		return nil, err
	}
	// Newer versions run the session in a background gateway, which would
	// outlive the run; stopping it is a no-op for older ones.
	cmd := exec.Command("/bin/sh", "-c", `"$0" "$@"; s=$?; "$0" gateway stop >/dev/null 2>&1; exit $s`,
		bin, "chat", "-Q", "-q", prompt, "--provider", "mock", "-m", "mock-model", "--yolo")
	cmd.Dir = dir
	cmd.Env = env
	return cmd, nil
}

func (Hermes) UserMessages(_, _ string) []string { return nil }
