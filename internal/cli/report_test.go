package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestShowSession(t *testing.T) {
	h := testenv.Home(t)
	svc := testenv.Repo(t, filepath.Join(h, "dev", "svc"), "git@github.com:acme/svc.git")
	other := testenv.Repo(t, filepath.Join(h, "dev", "other"), "git@github.com:acme/other.git")
	third := testenv.Repo(t, filepath.Join(h, "dev", "third"), "git@github.com:acme/third.git")
	for name, text := range map[string]string{"me.md": "MARK-ME", "shared.md": "MARK-SHARED", "svc.md": "MARK-SVC", "other.md": "MARK-OTHER", "third.md": "MARK-THIRD"} {
		testenv.Write(t, filepath.Join(h, "ctx", name), text)
	}
	for _, s := range []string{"deploy", "lint"} {
		testenv.Write(t, filepath.Join(h, "skills", s, "SKILL.md"), "---\ndescription: "+s+" it.\n---\n")
	}
	testenv.Config(t, h, `schema: ctxtra/v1
bindings:
  - name: me
    match: {global: true}
    instructions: [~/ctx/me.md]
  - name: svc
    match: {repos: [{remote: github.com/acme/svc}]}
    instructions: [~/ctx/shared.md, ~/ctx/svc.md]
    skills: [~/skills/deploy]
  - name: other
    match: {repos: [{remote: github.com/acme/other}]}
    instructions: [~/ctx/shared.md, ~/ctx/other.md]
    skills: [~/skills/deploy, ~/skills/lint]
  - name: third
    match: {repos: [{remote: github.com/acme/third}]}
    instructions: [~/ctx/shared.md, ~/ctx/other.md, ~/ctx/svc.md, ~/ctx/third.md]
`)

	// Only what the other repository adds: not the global file, nor the file
	// and skill that the session's repository has too.
	for _, args := range [][]string{{"show", other, "--session", svc}, {"show", "--session", svc, other}} {
		code, out, _ := run(args...)
		if code != 0 || !strings.Contains(out, "MARK-OTHER") || !strings.Contains(out, "<name>lint</name>") {
			t.Errorf("%v: %d\n%s", args, code, out)
		}
		for _, not := range []string{"MARK-ME", "MARK-SHARED", "MARK-SVC", "<name>deploy</name>"} {
			if strings.Contains(out, not) {
				t.Errorf("%v: has %s\n%s", args, not, out)
			}
		}
		if !strings.Contains(out, "for this repository, ~/dev/other,") {
			t.Errorf("%v: the header doesn't name the other repository\n%s", args, out)
		}
		// The file the session has from svc applies here too: listed
		// without its text.
		known := fmt.Sprintf("Files that ctxtra also adds for this repository, ~/dev/other, and all its worktrees, which this session already has:\n- %s (binding: other ", filepath.Join(h, "ctx", "shared.md"))
		if !strings.Contains(out, known) {
			t.Errorf("%v: no list of the files the session has\n%s", args, out)
		}
	}
	code, out, _ := run("show", filepath.Join(svc, "sub"), "--session", svc, "--part", "1")
	if want := "(nothing is added here beyond what sessions in the --session folders get)\n"; code != 0 || out != want {
		t.Errorf("same repository: %d %q, want %q", code, out, want)
	}

	// Several folders: what each of them gets is left out.
	code, out, _ = run("show", third, "--session", svc, "--session", other)
	if code != 0 || !strings.Contains(out, "MARK-THIRD") || strings.Contains(out, "MARK-SHARED") || strings.Contains(out, "MARK-OTHER") || strings.Contains(out, "MARK-SVC") ||
		!strings.Contains(out, "- "+filepath.Join(h, "ctx", "shared.md")) || !strings.Contains(out, "- "+filepath.Join(h, "ctx", "other.md")) {
		t.Errorf("two sessions: %d\n%s", code, out)
	}

	// A file the session has from a repository binding, which a global
	// binding adds too: the session doesn't know that it applies here.
	testenv.Config(t, h, `schema: ctxtra/v1
bindings:
  - name: svc
    match: {repos: [{remote: github.com/acme/svc}]}
    instructions: [~/ctx/svc.md]
  - name: all
    match: {global: true}
    instructions: [~/ctx/svc.md]
`)
	if _, out, _ := run("show", other, "--session", svc); strings.Contains(out, "MARK-SVC") || !strings.Contains(out, "- "+filepath.Join(h, "ctx", "svc.md")+" (binding: all ") {
		t.Errorf("global here, from a repository binding there:\n%s", out)
	}
	if _, out, _ := run("show", other, "--session", h); !strings.Contains(out, "(nothing is added here beyond") {
		t.Errorf("global here and there:\n%s", out)
	}
	testenv.Config(t, h, `schema: ctxtra/v1
bindings:
  - name: svc
    match: {repos: [{remote: github.com/acme/svc}]}
    instructions: [~/ctx/shared.md]
  - name: other
    match: {repos: [{remote: github.com/acme/other}]}
    instructions: [~/ctx/other.md]
`)

	// A text longer than a part: each part says how to get the next, and
	// fits the limit as printed, where control and invisible characters
	// become longer escapes.
	testenv.Write(t, filepath.Join(h, "ctx", "other.md"), strings.Repeat("MARK-OTHER line\u200b\u200b\u200b of a long file\n", 400))
	next := regexp.MustCompile(`\n\[ctxtra: part (\d+) of (\d+); for the next part, run the same command with --part (\d+)\]\n$`)
	var text strings.Builder
	for n := 1; ; n++ {
		code, out, errOut := run("show", other, "--session", svc, "--part", fmt.Sprint(n))
		if code != 0 {
			t.Fatalf("part %d: %d %s", n, code, errOut)
		}
		if len(out) > showPart {
			t.Errorf("part %d is %d bytes", n, len(out))
		}
		m := next.FindStringSubmatch(out)
		if m == nil {
			text.WriteString(out)
			if n < 3 {
				t.Fatalf("part %d is the last one; want 3 or more:\n%s", n, out)
			}
			break
		}
		if m[1] != fmt.Sprint(n) || m[3] != fmt.Sprint(n+1) {
			t.Fatalf("part %d: %q", n, m[0])
		}
		text.WriteString(out[:len(out)-len(m[0])])
	}
	if got := strings.Count(text.String(), `MARK-OTHER line\u200b\u200b\u200b of a long file`); got != 400 {
		t.Errorf("the parts have %d lines of the file, want 400", got)
	}
	if code, _, errOut := run("show", other, "--part", "99"); code != 2 || !strings.Contains(errOut, "--part 99: the text has") {
		t.Errorf("--part 99: %d %q", code, errOut)
	}

	// A file over an agent's limit: sessions of that agent don't get it, so
	// show gives its text.
	testenv.Write(t, filepath.Join(h, "ctx", "big.md"), strings.Repeat("MARK-BIG line of a big file\n", 400_000/28))
	testenv.Config(t, h, `schema: ctxtra/v1
bindings:
  - name: svc
    match: {repos: [{remote: github.com/acme/svc}]}
    instructions: [~/ctx/big.md]
  - name: other
    match: {repos: [{remote: github.com/acme/other}]}
    instructions: [~/ctx/big.md]
`)
	if _, out, _ := run("show", other, "--session", svc); !strings.Contains(out, "MARK-BIG") || strings.Contains(out, "Files that ctxtra also adds") {
		t.Errorf("over an agent's limit: the text isn't there\n%.500s", out)
	}
}

// A long title, such as a path of escapes, doesn't fill the header of each
// continued part.
func TestShowLongTitle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no control characters in Windows file names")
	}
	h := testenv.Home(t)
	other := testenv.Repo(t, filepath.Join(h, "other"), "git@github.com:acme/other.git")
	esc := strings.Repeat("\x1b", 240)
	file := filepath.Join(h, esc, esc, esc, "a.md")
	testenv.Write(t, file, strings.Repeat("MARK-LONG line of a file\n", 30_000/25))
	testenv.Config(t, h, `schema: ctxtra/v1
bindings:
  - name: other
    match: {repos: [{remote: github.com/acme/other}]}
    instructions: ["`+strings.ReplaceAll(file, "\x1b", `\e`)+`"]
`)
	for n := 1; ; n++ {
		code, out, errOut := run("show", other, "--part", fmt.Sprint(n))
		if code != 0 || !strings.Contains(out, "MARK-LONG") {
			t.Fatalf("part %d: %d %s\n%.300s", n, code, errOut, out)
		}
		if len(out) > showPart {
			t.Errorf("part %d is %d bytes", n, len(out))
		}
		if !strings.Contains(out, "for the next part") {
			break
		}
		if n == 6 {
			t.Fatalf("more than 6 parts for 30,000 bytes of text")
		}
	}
}
