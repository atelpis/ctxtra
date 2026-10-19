package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/hook"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/resolve"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// TestMain runs one hook, as a hook process: with CTXTRA_TEST_HOOK set, the
// test binary is ctxtra with the arguments after its name. A slot holds its
// lock until its process ends, so the slots of one event can't share a
// process.
func TestMain(m *testing.M) {
	if os.Getenv("CTXTRA_TEST_HOOK") != "" {
		os.Exit(Run(os.Args[1:], IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, "test"))
	}
	os.Exit(m.Run())
}

// shellHook is the argv after the binary in a hook command of any version.
var shellHook = regexp.MustCompile(`^/bin/sh -c 'f="\$HOME/bin/ctxtra"; if \[ -x "\$f" \]; then exec "\$f" ([^;'"]+); else `)

// TestClientContract: every hook command of every client fixture
// (testdata/clients, docs/spec.md, Setup compatibility) gets what its
// client can carry, from this version, below, near and above that amount.
// The amount is computed from client.json and the agent's Delivery, not
// stored, so that a server-only change reaches unchanged clients. Plugins
// run in the e2e tests.
func TestClientContract(t *testing.T) {
	ran := 0
	for _, c := range testenv.Clients(t) {
		t.Run(c.ID+"/"+c.Agent, func(t *testing.T) {
			a, ok := registry.Get(c.Agent)
			if !ok {
				t.Fatalf("unknown agent %q", c.Agent)
			}
			h := testenv.Home(t)
			// Not run: the test runs the hooks' argv itself.
			c.Load(t, h, "/bin/ctxtra")
			hooks := clientHooks(t, a, c)
			if len(hooks) == 0 {
				t.Skip("no hook commands")
			}
			ran++
			if c.Reads == "" {
				t.Fatal("client.json doesn't say which answer field the client reads")
			}
			d, notice := carries(t, a, c)
			dir := filepath.Join(h, "work")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			// Percent of what the client carries per file: all files fit
			// below and near it, the last one doesn't above it.
			for _, size := range []struct {
				name  string
				files []int
			}{
				{"below", []int{30}},
				{"near", []int{50, 42}},
				{"above", []int{50, 30, 30}},
			} {
				files := writeFiles(t, h, size.name, d, size.files)
				res, _ := resolve.Session(dir, paths.ConfigPath())
				d.Fit(res)
				if dropped := len(res.Dropped) > 0; dropped != (size.name == "above") {
					t.Fatalf("%s: %d of %d files over %s", size.name, len(res.Dropped), len(files), d.Over())
				}
				parts := len(d.Chunks(res))
				for _, ev := range c.Events {
					name := size.name + " " + ev
					in, _ := json.Marshal(map[string]string{"session_id": c.ID + "-" + name, "cwd": dir, "source": "startup", "hook_event_name": ev})
					var all []string
					for i, a := range runHooks(t, hooks[ev], string(in)) {
						got := clientText(t, c, ev, a)
						// Slots past the last part answer without text; a hook
						// without a slot has the whole text.
						if want := slotOf(hooks[ev][i]) < parts; (len(got) > 0) != want {
							t.Errorf("%s: %v: text %v, want %v", name, hooks[ev][i], len(got) > 0, want)
						}
						all = append(all, got...)
					}
					if c.Limit != nil {
						u, _ := c.Limit.ChunkUnit()
						if n := u.Len(strings.Join(all, "")); n > c.Limit.Value {
							t.Errorf("%s: %d %s, over the client's limit of %d", name, n, c.Limit.Unit, c.Limit.Value)
						}
					}
					lines := map[string]int{}
					for _, l := range strings.Split(strings.Join(all, "\n"), "\n") {
						lines[l]++
					}
					for _, f := range files {
						kept := slices.ContainsFunc(res.Files, func(h resolve.Hit) bool { return h.Path == f.path })
						once, seen := 0, 0
						for _, l := range f.lines {
							if lines[l] == 1 {
								once++
							}
							if lines[l] > 0 {
								seen++
							}
						}
						if kept && once != len(f.lines) {
							t.Errorf("%s: %s: %d of %d lines once, want all", name, filepath.Base(f.path), once, len(f.lines))
						}
						if !kept && seen != 0 {
							t.Errorf("%s: %s: %d of %d lines present, want none", name, filepath.Base(f.path), seen, len(f.lines))
						}
					}
					e := lastEntry(t, c.Agent, ev)
					var dropped []string
					for _, h := range res.Dropped {
						dropped = append(dropped, h.Path)
					}
					if !slices.Equal(e.Dropped, dropped) {
						t.Errorf("%s: log drops %v, want %v", name, e.Dropped, dropped)
					}
					told := slices.ContainsFunc(e.Problems, func(p string) bool { return strings.Contains(p, "run ctxtra setup") })
					if told != notice {
						t.Errorf("%s: notice to run setup %v, want %v: %v", name, told, notice, e.Problems)
					}
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("no client fixture with hook commands ran")
	}
}

// carries is what the client c can carry from this version: the agent's
// Delivery in the client's slots and under its limit. notice: a setup now
// would carry more.
func carries(t *testing.T, a agent.Agent, c testenv.Client) (d agent.Delivery, notice bool) {
	t.Helper()
	fresh := a.Delivery()
	d = fresh.WithSlots(c.Slots)
	if c.Limit != nil {
		if u, _ := c.Limit.ChunkUnit(); u != d.Unit {
			t.Fatalf("limit in %s, %s counts %s", c.Limit.Unit, d.Title, d.Unit)
		}
		d.Capacity = min(d.Capacity, c.Limit.Value)
	}
	return d, d.Capacity < fresh.Capacity || d.Slots < fresh.Slots
}

// clientHooks are the argv of the client's hook commands, by event. Each
// is one that setup recognizes as its own, so a later setup updates it in
// place.
func clientHooks(t *testing.T, a agent.Agent, c testenv.Client) map[string][][]string {
	t.Helper()
	m, _ := a.(agent.Missing)
	out := map[string][][]string{}
	for _, cmd := range fixtureCommands(t, c.Dir) {
		g := shellHook.FindStringSubmatch(cmd)
		h, ours := agent.ParseCommand(cmd, m)
		if g == nil || !ours || h.Binary != filepath.Join(paths.Home(), "bin", "ctxtra") {
			t.Fatalf("not a hook command of ctxtra's: %s", cmd)
		}
		argv := strings.Fields(g[1])
		if len(argv) < 3 || argv[0] != "hook" || argv[1] != c.Agent || !slices.Contains(c.Events, argv[2]) {
			t.Fatalf("%v: not a hook of %s for one of %v", argv, c.Agent, c.Events)
		}
		out[argv[2]] = append(out[argv[2]], argv)
	}
	for ev, hooks := range out {
		if want := max(c.Slots, 1); len(hooks) != want {
			t.Errorf("%s: %d hooks, client.json says %d", ev, len(hooks), want)
		}
	}
	if len(out) > 0 && len(out) != len(c.Events) {
		t.Errorf("hooks for %d events, client.json lists %v", len(out), c.Events)
	}
	return out
}

// fixtureCommands are the hook commands in the fixture's JSON files.
func fixtureCommands(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(f string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(f) != ".json" || f == filepath.Join(dir, "client.json") {
			return err
		}
		b, err := os.ReadFile(f)
		var v any
		if err == nil {
			err = json.Unmarshal(b, &v)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		var walk func(any)
		walk = func(v any) {
			switch v := v.(type) {
			case map[string]any:
				if cmd, ok := v["command"].(string); ok {
					out = append(out, cmd)
				}
				for _, k := range slices.Sorted(maps.Keys(v)) {
					walk(v[k])
				}
			case []any:
				for _, e := range v {
					walk(e)
				}
			}
		}
		walk(v)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// answer is a hook's answer as written to stdout, and parsed.
type answer struct {
	raw string
	v   any
}

// runHooks runs the hooks of one event at once, as the agents do, each in
// its own process, and returns their answers.
func runHooks(t *testing.T, hooks [][]string, in string) []answer {
	t.Helper()
	answers := make([]answer, len(hooks))
	var wg sync.WaitGroup
	for i, argv := range hooks {
		wg.Go(func() {
			cmd := exec.Command(os.Args[0], argv...)
			cmd.Env = append(os.Environ(), "CTXTRA_TEST_HOOK=1")
			cmd.Stdin = strings.NewReader(in)
			out, err := cmd.Output()
			answers[i].raw = string(out)
			if err == nil {
				err = json.Unmarshal(out, &answers[i].v)
			}
			if err != nil {
				t.Errorf("%v: %v: %q", argv, err, out)
			}
		})
	}
	wg.Wait()
	return answers
}

// slotOf is the hook's --slot, -1 without one.
func slotOf(argv []string) int {
	if i := slices.Index(argv, "--slot"); i >= 0 && i+1 < len(argv) {
		if n, err := strconv.Atoi(argv[i+1]); err == nil {
			return n
		}
	}
	return -1
}

// clientText is the text in the answer a that the client c reads, in
// parts, each within the client's part size. An answer with an event name
// names ev.
func clientText(t *testing.T, c testenv.Client, ev string, a answer) []string {
	t.Helper()
	m, ok := a.v.(map[string]any)
	if !ok {
		t.Errorf("%s: answer %s isn't an object", ev, short(a.v))
		return nil
	}
	if v, ok := m["hookSpecificOutput"]; ok {
		if out, ok := v.(map[string]any); !ok || out["hookEventName"] != ev {
			t.Errorf("%s: hookSpecificOutput %s doesn't name the event", ev, short(v))
		}
	}
	got, err := texts(m, strings.Split(c.Reads, "."))
	if err != nil {
		t.Errorf("%s: %s: %v", ev, c.Reads, err)
	}
	if c.Part == nil {
		return got
	}
	switch u, _ := c.Part.ChunkUnit(); u {
	case chunk.JSON:
		// The JSON document, without the line end after it.
		if n := len(strings.TrimSuffix(a.raw, "\n")); n > c.Part.Value {
			t.Errorf("%s: an answer of %d bytes, over the client's %d", ev, n, c.Part.Value)
		}
	default:
		for _, s := range got {
			if n := u.Len(s); n > c.Part.Value {
				t.Errorf("%s: a part of %d %s, over the client's %d", ev, n, c.Part.Unit, c.Part.Value)
			}
		}
	}
	return got
}

// texts are the strings at path in v, none when a key is missing. Objects
// are on the way, a string at the end; a key with [] has a list, with an
// element for each part.
func texts(v any, path []string) ([]string, error) {
	if len(path) == 0 {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s isn't a string", short(v))
		}
		return []string{s}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s isn't an object", short(v))
	}
	key, list := strings.CutSuffix(path[0], "[]")
	e, ok := m[key]
	if !ok {
		return nil, nil
	}
	if !list {
		return texts(e, path[1:])
	}
	l, ok := e.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s isn't a list", key, short(e))
	}
	var out []string
	for _, e := range l {
		got, err := texts(e, path[1:])
		if err == nil && len(got) == 0 {
			err = fmt.Errorf("%s: an element without the field", key)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// short is v for a message, without most of a long text.
func short(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

type file struct {
	path  string
	lines []string
}

// line has characters that the units count differently: outside the Basic
// Multilingual Plane (2 in UTF-16, 4 bytes), CJK (3 bytes), and ones that
// JSON escapes or might.
const line = "😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀 漢字漢字漢字漢字 \"q\" \\ <a&b> tab\tend"

// writeFiles writes a file per share, in percent of what d carries, and the
// config that binds them for every session.
func writeFiles(t *testing.T, h, name string, d agent.Delivery, shares []int) []file {
	t.Helper()
	part := d.Unit
	if d.ChunkJSON {
		part = chunk.JSON
	}
	cfg := "schema: ctxtra/v1\nbindings:\n  - name: contract\n    match: {global: true}\n    instructions:\n"
	var out []file
	for i, share := range shares {
		f := file{path: filepath.Join(h, "ctx", fmt.Sprintf("%s-%d.md", name, i))}
		var b strings.Builder
		// The share of the capacity, and of all slots' parts where the agent
		// has slots.
		for size, parts := 0, 0; size*100 < d.Capacity*share && (d.Slots == 0 || d.Chunk == 0 || parts*100 < d.Slots*d.Chunk*share); {
			l := fmt.Sprintf("ctxtra-contract %s F%d L%06d %s", name, i, len(f.lines), line)
			f.lines = append(f.lines, l)
			b.WriteString(l + "\n")
			size += d.Unit.Len(l + "\n")
			parts += part.Len(l + "\n")
		}
		testenv.Write(t, f.path, b.String())
		cfg += "      - " + f.path + "\n"
		out = append(out, f)
	}
	testenv.Config(t, h, cfg)
	return out
}

// lastEntry is the last entry of ctxtra's log for the agent and event.
func lastEntry(t *testing.T, harness, event string) hook.Entry {
	t.Helper()
	f, err := os.Open(paths.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var last *hook.Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		var e hook.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Harness == harness && e.Event == event {
			last = &e
		}
	}
	if last == nil {
		t.Fatalf("no log entry for %s %s", harness, event)
	}
	if last.Error != "" {
		t.Fatalf("log: %s", last.Error)
	}
	return *last
}
