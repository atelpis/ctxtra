package hook

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
)

// maxLogBytes is when the log is rotated to log.jsonl.1.
const maxLogBytes = 1 << 20

// Entry is one hook run in the log.
type Entry struct {
	Ts        string   `json:"ts"`
	Harness   string   `json:"harness"`
	Event     string   `json:"event"`
	Dir       string   `json:"dir,omitempty"`
	Repo      string   `json:"repo,omitempty"`
	Files     []string `json:"files,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	Missing   []string `json:"missing,omitempty"`
	Dropped   []string `json:"dropped,omitempty"`
	Problems  []string `json:"problems,omitempty"`
	Chars     int      `json:"chars"`
	Source    string   `json:"source,omitempty"`
	AgentType string   `json:"agent_type,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func newEntry(ts, harness, event string, p payload, res *resolve.Result, chars int) Entry {
	e := Entry{Ts: ts, Harness: harness, Event: event, Dir: res.Dir, Missing: res.Missing,
		Problems: res.Problems, Chars: chars, Source: p.Source, AgentType: p.AgentType}
	if res.Repo != nil {
		e.Repo = res.Repo.Root
	}
	for _, h := range res.Files {
		e.Files = append(e.Files, h.Path)
	}
	for _, h := range res.Skills {
		e.Skills = append(e.Skills, h.Path)
	}
	for _, h := range res.Dropped {
		e.Dropped = append(e.Dropped, h.Path)
	}
	return e
}

// logEntry appends e to the log. Errors are ignored and nothing blocks (the
// log can't be a FIFO): logging must never break a session.
func logEntry(e Entry) {
	path := paths.LogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() && st.Size() > maxLogBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := fsutil.OpenRegular(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
}

// LastRuns returns the last logged run per agent and event for sessions in
// dir (or in repo, when repo is set), sorted by agent and event.
func LastRuns(dir, repo string) []Entry {
	f, err := fsutil.OpenRegular(paths.LogPath(), os.O_RDONLY, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	last := map[string]Entry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if (repo != "" && e.Repo == repo) || (repo == "" && e.Dir == dir) {
			last[e.Harness+" "+e.Event] = e
		}
	}
	keys := make([]string, 0, len(last))
	for k := range last {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Entry, len(keys))
	for i, k := range keys {
		out[i] = last[k]
	}
	return out
}
