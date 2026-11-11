package notices

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "write THIRD_PARTY_NOTICES.md")

// The file the release archives include.
const file = "../../THIRD_PARTY_NOTICES.md"

// targets are the systems ctxtra ships for.
var targets = [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}}

type module struct {
	Path, Version, Dir string
	pkgs               map[string]bool // the folders of its linked packages
}

// list runs go list for target with format, one line per linked package.
func list(t *testing.T, target [2]string, format string, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", format, pkg)
	cmd.Env = append(os.Environ(), "GOOS="+target[0], "GOARCH="+target[1], "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list (%s/%s): %v", target[0], target[1], err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// modules are the modules linked into ctxtra on the systems it ships for,
// with the folders of the packages it links.
func modules(t *testing.T) []module {
	t.Helper()
	seen := map[string]*module{}
	for _, target := range targets {
		for _, line := range list(t, target, "{{with .Module}}{{.Path}} {{.Version}} {{.Dir}}{{end}}\t{{.Dir}}", "github.com/ctxtra/ctxtra/cmd/ctxtra") {
			mod, dir, _ := strings.Cut(line, "\t")
			f := strings.SplitN(mod, " ", 3)
			if len(f) != 3 || f[0] == "github.com/ctxtra/ctxtra" {
				continue
			}
			m := seen[f[0]]
			if m == nil {
				m = &module{Path: f[0], Version: f[1], Dir: f[2], pkgs: map[string]bool{}}
				seen[f[0]] = m
			}
			m.pkgs[dir] = true
		}
	}
	var out []module
	for _, m := range seen {
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b module) int { return strings.Compare(a.Path, b.Path) })
	return out
}

func isLicense(name string) bool {
	n := strings.ToUpper(name)
	return strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "LICENCE") || strings.HasPrefix(n, "COPYING") ||
		strings.HasPrefix(n, "NOTICE")
}

// licenseFiles are a module's license and notice files: at its root, and
// in the folders of its linked packages and their parents, where bundled
// code keeps its own.
func licenseFiles(m module) []string {
	dirs := map[string]bool{m.Dir: true}
	for d := range m.pkgs {
		for ; strings.HasPrefix(d, m.Dir) && d != m.Dir; d = filepath.Dir(d) {
			dirs[d] = true
		}
	}
	var out []string
	for d := range dirs {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if !e.IsDir() && isLicense(e.Name()) {
				out = append(out, filepath.Join(d, e.Name()))
			}
		}
	}
	slices.Sort(out)
	return out
}

// grants are words that start a license in a source comment.
var grants = []string{"Permission is hereby granted", "Permission to use, copy, modify", "Redistribution and use in source"}

// sourceNotices are the licenses that linked source files carry in their
// comments, apart from their module's own files: code bundled from
// elsewhere, such as the Inferno code in the Go runtime or the Bech32 code
// in age. Each comment block that grants a license, once, by the module
// (or "Go") whose files have it.
func sourceNotices(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	seen := map[string]bool{}
	format := `{{if .Module}}{{.Module.Path}}{{else}}Go{{end}}` + "\t" + `{{.Dir}}` + "\t" + `{{join .GoFiles " "}} {{join .SFiles " "}}`
	for _, target := range targets {
		for _, line := range list(t, target, format, "github.com/ctxtra/ctxtra/cmd/ctxtra") {
			f := strings.Split(line, "\t")
			if len(f) != 3 || f[0] == "github.com/ctxtra/ctxtra" {
				continue
			}
			for _, name := range strings.Fields(f[2]) {
				data, err := os.ReadFile(filepath.Join(f[1], name))
				if err != nil {
					t.Fatal(err)
				}
				for _, block := range commentBlocks(string(data)) {
					if !slices.ContainsFunc(grants, func(g string) bool { return strings.Contains(block, g) }) || seen[block] {
						continue
					}
					seen[block] = true
					out[f[0]] = append(out[f[0]], block)
				}
			}
		}
	}
	for k := range out {
		slices.Sort(out[k])
	}
	return out
}

// commentBlocks are the runs of // comment lines in a source file, without
// the comment markers.
func commentBlocks(src string) []string {
	var out, cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, l := range strings.Split(src, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "//") {
			flush()
			continue
		}
		cur = append(cur, strings.TrimPrefix(strings.TrimPrefix(l, "//"), " "))
	}
	flush()
	return out
}

func generate(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("# Third-party notices\n\nctxtra includes the following software. Each one's license follows.\n")
	block := func(text string) { fmt.Fprintf(&b, "\n```text\n%s\n```\n", strings.TrimRight(text, "\n")) }
	section := func(title string, paths []string) {
		fmt.Fprintf(&b, "\n## %s\n", title)
		for _, p := range paths {
			text, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			block(string(text))
		}
	}
	// The Go runtime and standard library are in every binary.
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	notices := sourceNotices(t)
	section("Go", []string{filepath.Join(strings.TrimSpace(string(goroot)), "LICENSE")})
	for _, n := range notices["Go"] {
		block(n)
	}
	for _, m := range modules(t) {
		paths := licenseFiles(m)
		if len(paths) == 0 {
			t.Errorf("%s %s has no license file", m.Path, m.Version)
		}
		section(m.Path+" "+m.Version, paths)
		for _, n := range notices[m.Path] {
			block(n)
		}
	}
	return b.Bytes()
}

// THIRD_PARTY_NOTICES.md names every module linked into ctxtra, with its
// licenses. After a dependency change: go test ./internal/notices -update
func TestNotices(t *testing.T) {
	want := generate(t)
	if *update {
		if err := os.WriteFile(file, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is out of date: run go test ./internal/notices -update", filepath.Base(file))
	}
}
