package e2e

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// capacity is how many characters of rendered ASCII text, headers
// included, each agent must get in full at session start. Claude and Muse
// cut or drop one hook's text above about 10K and 15K characters, so ctxtra
// splits the text across a fixed number of hooks there; the other agents get
// it from one hook or plugin. Copilot drops all of it when it doesn't fit
// the model's context window, Antigravity keeps only the last ~686K bytes.
func capacity(agent string) int {
	switch agent {
	case "claude", "muse":
		return 360_000
	case "copilot":
		return 400_000
	case "antigravity":
		return 650_000
	}
	return 1_000_000
}

// slotted are the agents that get the text from several hooks or callbacks
// of one session start: ctxtra resolves the files once for all of them.
var slotted = []string{"claude", "muse", "hermes"}

// A file is lines with a unique ID each. A line is the unit of these tests:
// ctxtra may add its own text between lines, never inside one.
type bigFile struct {
	path  string
	lines []string
}

var fillers = []string{
	"lorem ipsum dolor sit amet, consectetur adipiscing elit",
	"Привет, мир: конвейер метрик и хранилище временных рядов",
	"こんにちは世界、分散システムの観測性",
	"数据管道 · 基数 · 采样 · 告警",
	"naïve café déjà vu 🚀🧪📈 ∑∂√",
}

// emoji is outside the Basic Multilingual Plane: two UTF-16 units per
// character, which is how JavaScript agents count.
var emoji = strings.Repeat("🚀🧪📈", 12)

// writeBig writes a file of about chars characters (code points). With
// unicode, most lines are non-ASCII, so the file has more bytes than
// characters.
func writeBig(s *Session, rel, id string, chars int, unicode bool) bigFile {
	f := bigFile{path: s.Path(rel)}
	var b strings.Builder
	for n, size := 0, 0; size < chars; n++ {
		filler := fillers[0]
		switch {
		case id == "F5":
			filler = emoji
		case unicode:
			filler = fillers[1+n%(len(fillers)-1)]
		}
		line := fmt.Sprintf("ctxtra-capacity %s L%05d %s %s", id, n, filler, filler)
		f.lines = append(f.lines, line)
		b.WriteString(line + "\n")
		size += utf8.RuneCountInString(line) + 1
	}
	s.Write(rel, b.String())
	return f
}

// complete checks that every line of the files is in the text exactly once
// and that the lines appear in the order of files, then lines.
func complete(t *testing.T, name, text string, files ...bigFile) {
	t.Helper()
	pos := -1
	for _, f := range files {
		var missing, dup []int
		order := -1
		for i, l := range f.lines {
			switch n := strings.Count(text, l); {
			case n == 0:
				missing = append(missing, i)
				continue
			case n > 1:
				dup = append(dup, i)
			}
			if p := strings.Index(text, l); p < pos && order < 0 {
				order = i
			} else {
				pos = p
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s: %s: %d of %d lines missing, first %v", name, f.path, len(missing), len(f.lines), missing[:min(5, len(missing))])
		}
		if len(dup) > 0 {
			t.Errorf("%s: %s: %d lines appear more than once, first %v", name, f.path, len(dup), dup[:min(5, len(dup))])
		}
		if order >= 0 {
			t.Errorf("%s: %s: line %d comes before text it should follow", name, f.path, order)
		}
	}
}

// absent checks that no line of the file is in the text.
func absent(t *testing.T, name, text string, f bigFile) {
	t.Helper()
	n := 0
	for _, l := range f.lines {
		if strings.Contains(text, l) {
			n++
		}
	}
	if n > 0 {
		t.Errorf("%s: %s: %d of %d lines present, want none", name, f.path, n, len(f.lines))
	}
}

// capacityFiles binds about 305K characters in five files across two
// bindings: a large file that doesn't fit one hook of any agent, Unicode
// text, emoji-heavy text, and small files around them. Copilot gets half:
// its capacity counts tokens, and non-ASCII text has many.
func capacityFiles(s *Session) []bigFile {
	n := func(chars int) int {
		if s.agent.Name() == "copilot" {
			return chars / 2
		}
		return chars
	}
	files := []bigFile{
		writeBig(s, "ctx/me.md", "F1", n(5_000), false),
		writeBig(s, "ctx/big.md", "F2", n(180_000), false),
		writeBig(s, "ctx/uni.md", "F3", n(40_000), true),
		writeBig(s, "ctx/team.md", "F4", n(60_000), false),
		writeBig(s, "ctx/emoji.md", "F5", n(20_000), true),
	}
	s.Config(global("me", "~/ctx/me.md", "~/ctx/big.md") + `  - name: svc
    match: {dirs: [~/dev/svc]}
    instructions: [~/ctx/uni.md, ~/ctx/team.md, ~/ctx/emoji.md]
`)
	return files
}

// Every line of about 305K characters of bound files, Unicode and emoji included,
// reaches the model at session start, once, in config order.
func TestCapacity(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		files := capacityFiles(s)
		s.Setup()
		r := s.Run(repo, driver.Options{})
		complete(t, "main[first]", r.First(mock.Main).text, files...)
		if slices.Contains(slotted, a.Name()) {
			runs := 0
			for _, e := range s.Log() {
				if e.Harness == a.Name() {
					runs++
				}
			}
			if runs != 1 {
				t.Errorf("%d entries in ctxtra's log, want 1 for one session start: %+v", runs, s.Log())
			}
		}
	})
}

// A subagent gets the same text in full, and the main agent still has it
// once after tool calls.
func TestCapacitySubagent(t *testing.T) {
	forEachAgent(t, []driver.Cap{driver.Subagent}, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", map[string]string{"internal/code.txt": "CTXMARK-CODE\n"})
		files := capacityFiles(s)
		s.Setup()
		r := s.Run(repo, driver.Options{}, mock.Read(s.Path("dev/svc/internal/code.txt")), mock.StartSubagent())
		if last := r.Last(mock.Main).text; a.Name() == "copilot" && strings.Contains(last, compacted) {
			t.Log("Copilot compacted the conversation, which drops the text: see TestCopilotCompactionDropsText")
		} else {
			complete(t, "main[last]", r.Last(mock.Main).Has("CODE").text, files...)
		}
		complete(t, "subagent[first]", r.First(mock.Subagent).text, files...)
	})
}

// Above the agent's capacity, whole files are dropped from the end, as with
// budget.max-chars: the files before them arrive in full, a dropped file
// not at all. The log lists it, and the user is told which file was dropped.
func TestOverCapacity(t *testing.T) {
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		s := newSession(t, a)
		repo := s.Repo("dev/svc", "", nil)
		part := capacity(a.Name()) * 2 / 5
		x := writeBig(s, "ctx/x.md", "X", part, false)
		y := writeBig(s, "ctx/y.md", "Y", part, false)
		z := writeBig(s, "ctx/z.md", "Z", part, false)
		s.Config(global("me", "~/ctx/x.md", "~/ctx/y.md", "~/ctx/z.md"))
		s.Setup()
		r := s.Run(repo, driver.Options{})
		text := r.First(mock.Main).text
		complete(t, "main[first]", text, x, y)
		absent(t, "main[first]", text, z)
		logged := false
		for _, e := range s.Log() {
			logged = logged || slices.ContainsFunc(e.Dropped, func(p string) bool { return strings.HasSuffix(p, "/ctx/z.md") })
		}
		if !logged {
			t.Errorf("ctxtra's log doesn't list z.md as dropped: %+v", s.Log())
		}
		if driver.Has(a.Driver, driver.UserMessage) {
			msgs := strings.Join(a.UserMessages(s.Home, r.Stdout), "\n")
			if !strings.Contains(msgs, "dropped") || !strings.Contains(msgs, "z.md") {
				t.Errorf("user messages %q don't say that z.md was dropped", msgs)
			}
		}
	})
}
