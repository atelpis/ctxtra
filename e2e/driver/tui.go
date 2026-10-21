package driver

import (
	"os"
	"strings"
)

var (
	_ TUI = Claude{}
	_ TUI = Codex{}
	_ TUI = Pi{}
	_ TUI = OpenCode{}
	_ TUI = Copilot{}
	_ TUI = Muse{}
	_ TUI = Antigravity{}
	_ TUI = Hermes{}
)

func (Claude) Interactive(_, _, mockURL string, env []string) (Interactive, error) {
	return Interactive{Env: append(env, "ANTHROPIC_BASE_URL="+mockURL, "ANTHROPIC_API_KEY=sk-e2e-mock",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1")}, nil
}

func (Claude) Answer(screen string) []string {
	switch {
	case strings.Contains(screen, "Choose the text style"), strings.Contains(screen, "Security notes:"):
		return []string{"Enter"}
	case strings.Contains(screen, "Do you want to use this API key?") && strings.Contains(screen, "❯ No"):
		return []string{"Up", "Enter"} // Yes: the mock's key
	case strings.Contains(screen, "Yes, I trust this folder") && strings.Contains(screen, "❯ No, exit"):
		return []string{"Down", "Enter"}
	}
	return nil
}

func (Codex) Interactive(_, _, mockURL string, env []string) (Interactive, error) {
	sandbox := os.Getenv("CTXTRA_E2E_CODEX_SANDBOX")
	if sandbox == "" {
		sandbox = "read-only"
	}
	provider := `model_providers.mock={name="mock",base_url="` + mockURL + `/v1",wire_api="responses",env_key="E2E_MOCK_KEY"}`
	return Interactive{
		Pre: []string{"-s", sandbox, "-c", "model_provider=mock", "-c", "model=mock-model", "-c", provider,
			"-c", "features.plugins=false", "-c", "check_for_update_on_startup=false"},
		Env: append(env, "E2E_MOCK_KEY=mock"),
	}, nil
}

func (Codex) Answer(screen string) []string {
	switch {
	case strings.Contains(screen, "Trust this folder?"):
		return []string{"Enter"} // 1. Trust and continue
	case strings.Contains(screen, "Hooks need review"):
		return []string{"Down", "Enter"} // 2. Trust all and continue
	}
	return nil
}

func (Pi) Interactive(_, home, mockURL string, env []string) (Interactive, error) {
	if err := piModels(home, mockURL); err != nil {
		return Interactive{}, err
	}
	return Interactive{Pre: []string{"--no-session", "--model", "mock/mock-model"},
		Env: append(env, "PI_OFFLINE=1", "PI_TELEMETRY=0")}, nil
}

func (Pi) Answer(string) []string { return nil }

func (OpenCode) Interactive(_, home, mockURL string, env []string) (Interactive, error) {
	if err := openCodeConfig(home, mockURL); err != nil {
		return Interactive{}, err
	}
	return Interactive{Pre: []string{"--standalone"}, Env: append(env, "E2E_MOCK_KEY=mock",
		"OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_DISABLE_FILEWATCHER=1")}, nil
}

func (OpenCode) Answer(string) []string { return nil }

func (Copilot) Interactive(_, _, mockURL string, env []string) (Interactive, error) {
	return Interactive{Pre: []string{"--no-auto-update"}, Env: append(env,
		"COPILOT_PROVIDER_BASE_URL="+mockURL+"/v1", "COPILOT_PROVIDER_API_KEY=mock", "COPILOT_MODEL=mock-model",
		"COPILOT_OFFLINE=true")}, nil
}

func (Copilot) Answer(screen string) []string {
	if strings.Contains(screen, "Do you trust the files in this folder?") {
		return []string{"Enter"} // 1. Yes
	}
	return nil
}

func (Muse) Interactive(_, _, mockURL string, env []string) (Interactive, error) {
	return Interactive{Pre: []string{"--base-url", mockURL + "/v1", "--model", "mock-model"},
		Env: append(env, "META_API_KEY=mock", "MUSE_NO_AUTO_UPDATE=1")}, nil
}

func (Muse) Answer(screen string) []string {
	if strings.Contains(screen, "Do you trust this workspace?") {
		return []string{"1", "Enter"} // Trust and continue
	}
	return nil
}

func (Antigravity) Interactive(_, home, mockURL string, env []string) (Interactive, error) {
	if err := agySettings(home); err != nil {
		return Interactive{}, err
	}
	return Interactive{Env: append(env, "GEMINI_API_KEY=mock", "GOOGLE_GEMINI_BASE_URL="+mockURL,
		"AGY_CLI_DISABLE_AUTO_UPDATE=true")}, nil
}

func (Antigravity) Answer(string) []string { return nil }

func (Hermes) Interactive(_, home, mockURL string, env []string) (Interactive, error) {
	if err := hermesConfig(home, mockURL); err != nil {
		return Interactive{}, err
	}
	return Interactive{Post: []string{"--provider", "mock", "-m", "mock-model"}, Env: env}, nil
}

func (Hermes) Answer(string) []string { return nil }
