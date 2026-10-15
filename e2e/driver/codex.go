package driver

import (
	"os"
	"os/exec"
)

// Codex is Codex CLI, run with `codex exec`. Model requests go to a mock
// model provider (Responses API).
type Codex struct{}

func (Codex) Name() string       { return "codex" }
func (Codex) Executable() string { return "codex" }
func (Codex) Caps() []Cap        { return []Cap{Subagent, Untrusted} }

func (Codex) Command(bin, home, dir, mockURL, prompt string, o Options, env []string) (*exec.Cmd, error) {
	provider := `model_providers.mock={name="mock",base_url="` + mockURL + `/v1",wire_api="responses",env_key="E2E_MOCK_KEY"}`
	// The only command Codex runs is the mock's scripted `cat`. Its Linux
	// sandbox (bubblewrap) can't start in an unprivileged container, so the
	// Dockerfile turns it off with CTXTRA_E2E_CODEX_SANDBOX; the container,
	// without network, is the sandbox then.
	sandbox := os.Getenv("CTXTRA_E2E_CODEX_SANDBOX")
	if sandbox == "" {
		sandbox = "read-only"
	}
	args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--json", "-s", sandbox,
		"-c", "model_provider=mock", "-c", "model=mock-model", "-c", provider,
		// No plugin sync from GitHub at startup: the only network call
		// left would be to the mock.
		"-c", "features.plugins=false"}
	if !o.Untrusted {
		args = append(args, "--dangerously-bypass-hook-trust")
	}
	cmd := exec.Command(bin, append(args, prompt)...)
	cmd.Dir = dir
	cmd.Env = append(env, "E2E_MOCK_KEY=mock")
	return cmd, nil
}

func (Codex) UserMessages(string) []string { return nil }
