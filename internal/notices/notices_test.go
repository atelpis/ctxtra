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

type module struct {
	Path, Version, Dir string
}

// modules are the modules linked into ctxtra on the systems it ships for.
func modules(t *testing.T) []module {
	t.Helper()
	seen := map[string]module{}
	for _, goos := range []string{"darwin", "linux"} {
		cmd := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}} {{.Version}} {{.Dir}}{{end}}", "github.com/ctxtra/ctxtra/cmd/ctxtra")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=arm64", "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list (%s): %v", goos, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.SplitN(line, " ", 3)
			if len(f) == 3 && f[0] != "github.com/ctxtra/ctxtra" {
				seen[f[0]] = module{f[0], f[1], f[2]}
			}
		}
	}
	var out []module
	for _, m := range seen {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b module) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// licenseFiles are a module's license and notice files.
func licenseFiles(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		n := strings.ToUpper(e.Name())
		if !e.IsDir() && (strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "LICENCE") ||
			strings.HasPrefix(n, "COPYING") || strings.HasPrefix(n, "NOTICE")) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(out)
	return out
}

func generate(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("# Third-party notices\n\nctxtra includes the following software. Each one's license follows.\n")
	section := func(title string, paths []string) {
		fmt.Fprintf(&b, "\n## %s\n", title)
		for _, p := range paths {
			text, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "\n```text\n%s\n```\n", strings.TrimRight(string(text), "\n"))
		}
	}
	// The Go runtime and standard library are in every binary.
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	section("Go", []string{filepath.Join(strings.TrimSpace(string(goroot)), "LICENSE")})
	for _, m := range modules(t) {
		paths := licenseFiles(m.Dir)
		if len(paths) == 0 {
			t.Errorf("%s %s has no license file", m.Path, m.Version)
		}
		section(m.Path+" "+m.Version, paths)
	}
	return b.Bytes()
}

// THIRD_PARTY_NOTICES.md names every module linked into ctxtra, with its
// license. After a dependency change: go test ./internal/notices -update
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
