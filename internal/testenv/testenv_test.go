package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A test started from a git hook inherits GIT_DIR and friends. Home must
// clear them, or fixture commits would go into that repository.
func TestHomeIsolatesGit(t *testing.T) {
	// Isolate first: this test itself may run with GIT_DIR set.
	Home(t)
	outer := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", outer).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, k := range GitLocationVars {
		t.Setenv(k, filepath.Join(outer, ".git"))
	}
	h := Home(t)
	for _, k := range GitLocationVars {
		if _, ok := os.LookupEnv(k); ok {
			t.Errorf("%s is still set", k)
		}
	}
	Repo(t, filepath.Join(h, "r"), "")
	if out, err := exec.Command("git", "-C", outer, "rev-parse", "--verify", "-q", "HEAD").Output(); err == nil {
		t.Errorf("fixture committed into the outer repository: %s", out)
	}
}
