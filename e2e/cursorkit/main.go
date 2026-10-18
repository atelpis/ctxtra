// Command cursorkit checks by hand what ctxtra's text reaches the model in
// Cursor (the Agent CLI and the desktop app), whose model can't be mocked.
//
// A run binds one generated file with random codewords at known places to a
// test folder, through a project hook in the folder's .cursor/hooks.json, so
// the user's own config isn't changed. The model is asked to list the
// codewords without tools: a codeword can't be guessed, so each one in the
// answer proves that part of the text arrived. The answer and the tool calls
// are read from Cursor's transcript.
//
//	go run ./cursorkit prepare -dir DIR -chars 100000 [-delay 3s]
//	go run ./cursorkit cli -dir DIR          # runs cursor-agent, then verify
//	go run ./cursorkit verify -dir DIR       # after a desktop chat in DIR/workspace
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const canaries = 30

// run is what prepare writes for cli and verify.
type run struct {
	ID       string            `json:"id"`
	Chars    int               `json:"chars"`
	Delay    string            `json:"delay"`
	Canaries map[string]string `json:"canaries"` // CANARY-NN → codeword
	Start    time.Time         `json:"start"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cursorkit prepare|cli|verify -dir DIR")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	dir := fs.String("dir", "", "test folder (created by prepare)")
	chars := fs.Int("chars", 100_000, "prepare: size of the bound file in characters")
	delay := fs.Duration("delay", 0, "prepare: how long the hook waits before it answers")
	model := fs.String("model", "auto", "cli: cursor-agent model")
	fs.Parse(os.Args[2:])
	if *dir == "" {
		fail(errors.New("-dir is required"))
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		fail(err)
	}
	switch os.Args[1] {
	case "prepare":
		err = prepare(abs, *chars, *delay)
	case "cli":
		err = cli(abs, *model)
	case "verify":
		err = verify(abs)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "cursorkit:", err)
	os.Exit(1)
}

var words = strings.Fields(`amber basalt cobalt delta ember fjord garnet harbor indigo juniper kelp lichen
	marble nectar onyx pewter quartz russet sable tundra umber velvet walnut xenon yarrow zephyr`)

func pick(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}

func codeword() string {
	return fmt.Sprintf("%s-%s-%04d", words[pick(len(words))], words[pick(len(words))], pick(10000))
}

// prepare builds ctxtra into DIR/bin, writes the bound file with codewords
// spread from its first line to its last, the ctxtra config, and the
// project hook of DIR/workspace.
func prepare(dir string, chars int, delay time.Duration) error {
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s exists; use a new folder for each run", dir)
	}
	for _, d := range []string{"bin", "ctx", "config", "empty", "workspace/.cursor"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	bin := filepath.Join(dir, "bin", "ctxtra")
	build := exec.Command("go", "build", "-o", bin, "./cmd/ctxtra")
	build.Dir = ".." // the repository root, from e2e/
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building ctxtra: %v\n%s", err, out)
	}

	r := run{ID: fmt.Sprintf("ckit-%06d", pick(1_000_000)), Chars: chars, Delay: delay.String(),
		Canaries: map[string]string{}, Start: time.Now()}
	const lineLen = 100
	lines := max(chars/lineLen, canaries)
	every := float64(lines-1) / float64(canaries-1)
	at := map[int]string{}
	for i := range canaries {
		name := fmt.Sprintf("CANARY-%02d", i+1)
		r.Canaries[name] = codeword()
		at[int(float64(i)*every+0.5)] = name
	}
	var b strings.Builder
	filler := strings.Repeat("lorem ipsum dolor sit amet ", lineLen/27+2)
	for n := range lines {
		line := fmt.Sprintf("L%06d ", n)
		if name, ok := at[n]; ok {
			line += fmt.Sprintf("%s = %s ", name, r.Canaries[name])
		}
		b.WriteString(line + filler[:max(lineLen-len(line)-1, 0)] + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "ctx", "big.md"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	cfg := "schema: ctxtra/v1\nbindings:\n  - name: cursorkit\n    match: {global: true}\n    instructions: [" +
		filepath.Join(dir, "ctx", "big.md") + "]\n"
	if err := os.WriteFile(filepath.Join(dir, "config", "config.yaml"), []byte(cfg), 0o644); err != nil {
		return err
	}
	// For the user's own ctxtra hook, if any: binds nothing.
	if err := os.WriteFile(filepath.Join(dir, "empty", "config.yaml"), []byte("schema: ctxtra/v1\n"), 0o644); err != nil {
		return err
	}
	wait := ""
	if delay > 0 {
		wait = fmt.Sprintf("sleep %.3f; ", delay.Seconds())
	}
	cmd := fmt.Sprintf("/bin/sh -c '%sCTXTRA_CONFIG=%s CTXTRA_LOG=%s exec %s hook cursor sessionStart'",
		wait, filepath.Join(dir, "config", "config.yaml"), filepath.Join(dir, "log.jsonl"), bin)
	hooks, _ := json.MarshalIndent(map[string]any{"version": 1, "hooks": map[string]any{
		"sessionStart": []any{map[string]any{"command": cmd, "timeout": 30}}}}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "workspace", ".cursor", "hooks.json"), hooks, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace", "README.md"), []byte("cursorkit test folder\n"), 0o644); err != nil {
		return err
	}
	j, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "run.json"), j, 0o644); err != nil {
		return err
	}
	fmt.Printf("prepared %s: %d characters, %d codewords, hook delay %s\n", dir, b.Len(), canaries, delay)
	fmt.Printf("folder for Cursor: %s\nprompt (one line):\n%s\n", filepath.Join(dir, "workspace"), prompt(r))
	return nil
}

func prompt(r run) string {
	return r.ID + ": Your context has text added by ctxtra with codewords written as CANARY-NN = word-word-NNNN. " +
		"Without using any tool and without reading any file, list every codeword you can see in your context, " +
		"one per line as CANARY-NN = codeword, in order. If you see none, answer NONE."
}

func load(dir string) (run, error) {
	var r run
	b, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

// cli runs a headless cursor-agent chat in the workspace, then verify. The
// user's own ctxtra hook, if it reads CTXTRA_CONFIG, binds nothing.
func cli(dir, model string) error {
	r, err := load(dir)
	if err != nil {
		return err
	}
	cmd := exec.Command("cursor-agent", "-p", "--trust", "--model", model, "--workspace", filepath.Join(dir, "workspace"),
		"--output-format", "text", prompt(r))
	cmd.Dir = filepath.Join(dir, "workspace")
	cmd.Env = append(os.Environ(), "CTXTRA_CONFIG="+filepath.Join(dir, "empty", "config.yaml"))
	out, err := cmd.CombinedOutput()
	if err := os.WriteFile(filepath.Join(dir, "cli-output.txt"), out, 0o644); err != nil {
		return err
	}
	if err != nil {
		return fmt.Errorf("cursor-agent: %v\n%s", err, out)
	}
	return verify(dir)
}

var canaryRe = regexp.MustCompile(`CANARY-(\d\d)\s*=\s*([a-z]+-[a-z]+-\d{4})`)

// verify finds the newest Cursor transcript with the run's prompt, and
// reports the codewords in the answers and any tool call.
func verify(dir string) error {
	r, err := load(dir)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	paths, _ := filepath.Glob(filepath.Join(home, ".cursor", "projects", "*", "agent-transcripts", "*", "*.jsonl"))
	// Cursor names a project folder after the workspace path, shortened
	// with a hash when it is long: a chat opened in another folder has the
	// run's prompt too, but not this start.
	ws := strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(strings.TrimPrefix(filepath.Join(dir, "workspace"), "/"))
	ws = regexp.MustCompile(`-+`).ReplaceAllString(ws, "-")
	prefix := ws[:min(len(ws), 40)]
	var found string
	var newest time.Time
	for _, p := range paths {
		if project := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(p)))); !strings.HasPrefix(project, prefix) {
			continue
		}
		st, err := os.Stat(p)
		if err != nil || st.ModTime().Before(r.Start) || !st.ModTime().After(newest) {
			continue
		}
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), r.ID) {
			found, newest = p, st.ModTime()
		}
	}
	if found == "" {
		return fmt.Errorf("no Cursor transcript with %s since %s", r.ID, r.Start.Format(time.TimeOnly))
	}
	f, err := os.Open(found)
	if err != nil {
		return err
	}
	defer f.Close()
	var answer strings.Builder
	var tools []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Role    string `json:"role"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Role != "assistant" {
			continue
		}
		for _, c := range line.Message.Content {
			switch c.Type {
			case "text":
				answer.WriteString(c.Text + "\n")
			case "tool_use":
				tools = append(tools, c.Name)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	got := map[string]string{}
	for _, m := range canaryRe.FindAllStringSubmatch(answer.String(), -1) {
		got["CANARY-"+m[1]] = m[2]
	}
	var ok, wrong, missing []string
	for i := range canaries {
		name := fmt.Sprintf("CANARY-%02d", i+1)
		switch g, seen := got[name]; {
		case !seen:
			missing = append(missing, name[7:])
		case g == r.Canaries[name]:
			ok = append(ok, name[7:])
		default:
			wrong = append(wrong, name[7:])
		}
	}
	hook := "no ctxtra log: the project hook didn't run"
	if b, err := os.ReadFile(filepath.Join(dir, "log.jsonl")); err == nil {
		hook = fmt.Sprintf("ctxtra hook ran %d time(s)", strings.Count(string(b), "\n"))
	}
	fmt.Printf("run %s: %d characters, hook delay %s\ntranscript: %s\n%s\n", r.ID, r.Chars, r.Delay, found, hook)
	fmt.Printf("codewords: %d of %d right", len(ok), canaries)
	if len(wrong) > 0 {
		fmt.Printf(", wrong: %v", wrong)
	}
	if len(missing) > 0 {
		fmt.Printf(", missing: %v", missing)
	}
	fmt.Println()
	if len(tools) > 0 {
		fmt.Printf("WARNING: the model called tools %v, so the codewords may come from a file it read\n", tools)
	}
	if len(ok) == canaries && len(tools) == 0 {
		fmt.Println("RESULT: all sampled codewords verified")
	} else {
		fmt.Println("RESULT: not proven")
	}
	return nil
}
