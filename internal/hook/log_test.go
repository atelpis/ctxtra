package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctxtra/ctxtra/internal/paths"
	"github.com/ctxtra/ctxtra/internal/testenv"
)

func writeLog(t *testing.T, path string, lines ...any) {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		if s, ok := l.(string); ok {
			b.WriteString(s)
		} else {
			j, err := json.Marshal(l)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(j)
		}
		b.WriteByte('\n')
	}
	testenv.Write(t, path, b.String())
}

func runsString(runs []Entry) string {
	var out []string
	for _, e := range runs {
		out = append(out, e.Harness+" "+e.Event+" "+e.Ts)
	}
	return strings.Join(out, ", ")
}

func TestLastRunsRotated(t *testing.T) {
	h := testenv.Home(t)
	dir := filepath.Join(h, "svc")
	other := filepath.Join(h, "other")
	log := paths.LogPath()
	run := func(harness, event, ts, dir string) Entry {
		return Entry{Ts: ts, Harness: harness, Event: event, Dir: dir}
	}

	tests := []struct {
		name             string
		rotated, current []any
		want             string
	}{
		{
			name:    "only in the rotated log",
			rotated: []any{run("codex", "SessionStart", "1", dir), run("claude", "SessionStart", "2", dir)},
			want:    "claude SessionStart 2, codex SessionStart 1",
		},
		{
			name:    "the current log wins",
			rotated: []any{run("claude", "SessionStart", "1", dir), run("codex", "SessionStart", "2", dir)},
			current: []any{run("claude", "SessionStart", "3", dir), run("claude", "SessionStart", "4", other)},
			want:    "claude SessionStart 3, codex SessionStart 2",
		},
		{
			name:    "malformed lines",
			rotated: []any{"{not json", run("claude", "SessionStart", "1", dir), ""},
			current: []any{run("codex", "SessionStart", "2", dir), `{"ts": 5`},
			want:    "claude SessionStart 1, codex SessionStart 2",
		},
		{
			name:    "no rotated log",
			current: []any{run("claude", "SessionStart", "1", dir)},
			want:    "claude SessionStart 1",
		},
		{name: "no logs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Remove(log)
			os.Remove(log + ".1")
			if tt.rotated != nil {
				writeLog(t, log+".1", tt.rotated...)
			}
			if tt.current != nil {
				writeLog(t, log, tt.current...)
			}
			if got := runsString(LastRuns(dir, "")); got != tt.want {
				t.Errorf("runs %q, want %q", got, tt.want)
			}
		})
	}
}

// One log that can't be read doesn't hide the runs in the other.
func TestLastRunsUnreadableLog(t *testing.T) {
	h := testenv.Home(t)
	log := paths.LogPath()
	repo := filepath.Join(h, "repo")
	e := Entry{Ts: "1", Harness: "claude", Event: "SessionStart", Dir: filepath.Join(repo, "sub"), Repo: repo}

	if err := os.MkdirAll(log+".1", 0o755); err != nil {
		t.Fatal(err)
	}
	writeLog(t, log, e)
	if got := runsString(LastRuns("", repo)); got != "claude SessionStart 1" {
		t.Errorf("rotated log is a folder: runs %q", got)
	}

	os.Remove(log + ".1")
	os.Remove(log)
	writeLog(t, log+".1", e)
	if err := os.MkdirAll(log, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := runsString(LastRuns("", repo)); got != "claude SessionStart 1" {
		t.Errorf("current log is a folder: runs %q", got)
	}
}
