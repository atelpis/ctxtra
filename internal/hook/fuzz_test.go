package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
)

func FuzzInvalidInput(f *testing.F) {
	for _, s := range []string{"{", "null trailing", `{"cwd":1}`, `{"workspace_roots":"/tmp"}`, `{"ctxtra_plugin":[]}`, `{"ctxtra_slots":`, "\xff"} {
		f.Add([]byte(s))
	}
	f.Setenv("CTXTRA_LOG", os.DevNull)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(bytes.TrimSpace(data)) == 0 || len(data) > maxInput {
			return
		}
		var p payload
		if json.Unmarshal(data, &p) == nil {
			return
		}
		var out bytes.Buffer
		if code := Run("cursor", "sessionStart", bytes.NewReader(data), &out, io.Discard); code != 0 {
			t.Fatalf("invalid input failed the session: %d", code)
		}
		if out.String() != "{}\n" {
			t.Fatalf("invalid input added output: %q", out.String())
		}
	})
}
