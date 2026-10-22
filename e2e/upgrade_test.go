package e2e

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/e2e/driver"
	"github.com/atelpis/ctxtra/e2e/fixture"
	"github.com/atelpis/ctxtra/e2e/mock"
)

// clients is testdata/clients at the repository root: what each release
// set up for each agent (docs/spec.md, Setup compatibility).
const clients = "../testdata/clients"

// After an upgrade, a setup that a release wrote runs this version: the
// session gets every file up to what the setup can carry, files over that
// are dropped whole from the end, and the log and the user are told to run
// setup only when a setup now would carry more.
func TestUpgradeFromRelease(t *testing.T) {
	all, err := fixture.Clients(clients)
	if err != nil {
		t.Fatal(err)
	}
	forEachAgent(t, nil, func(t *testing.T, a Agent) {
		ran := false
		for _, c := range all {
			if c.Agent != a.Name() {
				continue
			}
			ran = true
			carry, notice := carries(a.Name(), c)
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
				t.Run(c.ID+"/"+size.name, func(t *testing.T) {
					upgraded(t, a, c, carry, size.files, size.name == "above", notice)
				})
			}
		}
		if !ran {
			t.Skip("no client fixture")
		}
	})
}

// upgraded runs a session of the client c with a file per share, in
// percent of carry, and checks what reaches the model, the log and the
// user: the last file is dropped when over, and the user is told to run
// setup when notice.
func upgraded(t *testing.T, a Agent, c fixture.Client, carry int, shares []int, over, notice bool) {
	s := newSession(t, a)
	repo := s.Repo("dev/svc", "", nil)
	var files []bigFile
	var bound []string
	for i, share := range shares {
		rel := fmt.Sprintf("ctx/u%d.md", i)
		files = append(files, writeBig(s, rel, fmt.Sprintf("U%d", i), carry*share/100, false))
		bound = append(bound, "~/"+rel)
	}
	s.Config(global("me", bound...))
	s.LoadClient(c.Dir)
	r := s.Run(repo, driver.Options{})
	text := r.First(mock.Main).text
	kept := files
	if over {
		kept = files[:len(files)-1]
		absent(t, "main[first]", text, files[len(files)-1])
	}
	complete(t, "main[first]", text, kept...)
	last := filepath.Base(files[len(files)-1].path)
	dropped, told := false, false
	for _, e := range s.Log() {
		if e.Harness != a.Name() {
			continue
		}
		dropped = dropped || slices.ContainsFunc(e.Dropped, func(p string) bool { return strings.HasSuffix(p, "/ctx/"+last) })
		told = told || slices.ContainsFunc(e.Problems, func(p string) bool { return strings.Contains(p, "run ctxtra setup") })
	}
	if dropped != over || told != notice {
		t.Errorf("ctxtra's log: %s dropped %v, want %v; told to run setup %v, want %v: %+v", last, dropped, over, told, notice, s.Log())
	}
	if driver.Has(a.Driver, driver.UserMessage) {
		msgs := strings.Join(a.UserMessages(s.Home, r.Stdout), "\n")
		if strings.Contains(msgs, "dropped "+last) != over || strings.Contains(msgs, "run ctxtra setup") != notice {
			t.Errorf("user messages %q: want %s dropped %v, told to run setup %v", msgs, last, over, notice)
		}
	}
}

// carries is how many characters of ASCII text the client c can carry from
// this version, as internal/cli/contract_test.go computes it: the agent's
// capacity, in the client's slots and under its limit. ASCII has as many
// bytes and UTF-16 units as characters. notice: a setup now would carry
// more.
func carries(agent string, c fixture.Client) (n int, notice bool) {
	n = capacity(agent)
	fresh, part := slots(agent)
	if c.Slots > 0 && c.Slots < fresh {
		n = min(n, c.Slots*part)
	}
	if c.Limit != nil {
		n = min(n, c.Limit.Value)
	}
	return n, n < capacity(agent) || c.Slots > 0 && c.Slots < fresh
}
