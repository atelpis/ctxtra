package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// compacted is how Copilot starts the history it keeps after compacting.
const compacted = "Some of the conversation history has been summarized"

// A known limit of Copilot, not of ctxtra: when Copilot compacts a long
// conversation, the text that the session-start hook added is gone for the
// rest of the session, and Copilot doesn't run the hook again. ctxtra's
// capacity for Copilot leaves room for the conversation, so this test makes
// Copilot compact with a hook of its own that adds more than ctxtra would.
// If it fails because the text survives, Copilot changed: update the docs
// (README "Limits", docs/spec.md "Capacity") and remove this test.
func TestCopilotCompactionDropsText(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		if a.Name() != "copilot" {
			t.Skip("Copilot only")
		}
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		f := writeBig(s, "ctx/big.md", "K", 500_000, false)
		b, _ := os.ReadFile(f.path)
		answer, _ := json.Marshal(map[string]any{"additionalContext": string(b)})
		s.Write("hook-answer.json", string(answer))
		hook, _ := json.Marshal(map[string]any{"version": 1, "hooks": map[string]any{"sessionStart": []any{
			map[string]any{"type": "command", "bash": "cat >/dev/null; cat " + s.Path("hook-answer.json"), "timeoutSec": 10}}}})
		s.Write(".copilot/hooks/big.json", string(hook))
		s.Write("dev/svc/a.txt", "CTXMARK-A\n")
		var script []mock.Step
		for range 6 {
			script = append(script, mock.Read(s.Path("dev/svc/a.txt")))
		}
		r := s.Run(repo, driver.Options{}, script...)
		main := r.View(mock.Main)
		complete(t, "main[first]", main[0].Text, f)
		after := -1
		for i, q := range main {
			if strings.Contains(q.Text, compacted) {
				after = i
				break
			}
		}
		if after < 0 {
			t.Fatalf("Copilot didn't compact in %d main requests", len(main))
		}
		absent(t, "main[after compaction]", main[after].Text, f)
	})
}
