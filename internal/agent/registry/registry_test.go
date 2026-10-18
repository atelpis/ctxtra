package registry

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/agent"
	"github.com/atelpis/ctxtra/internal/resolve"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// Every agent has what the commands need, and answers with a JSON object
// also when there is nothing to add.
func TestAgentsAreComplete(t *testing.T) {
	h := testenv.Home(t)
	seen := map[string]bool{}
	for _, a := range All() {
		name := a.Name()
		if name == "" || seen[name] {
			t.Errorf("bad or duplicate name %q", name)
		}
		seen[name] = true
		if got, ok := Get(name); !ok || got.Name() != name {
			t.Errorf("Get(%q)", name)
		}
		if p := a.ConfigPath(); !filepath.IsAbs(p) || !strings.HasPrefix(p, h+"/") {
			t.Errorf("%s: config path %q", name, p)
		}
		for _, res := range []*resolve.Result{nil, {}} {
			b, err := json.Marshal(a.Answer("SessionStart", res))
			var m map[string]any
			if err != nil || json.Unmarshal(b, &m) != nil {
				t.Errorf("%s: answer %s (%v)", name, b, err)
			}
		}
		if st := a.Status(); !st.OK || st.State != "not set up" {
			t.Errorf("%s: status %+v", name, st)
		}
		var r agent.Report
		if err := a.Setup(agent.Options{Binary: "/bin/ctxtra", DryRun: true}, &r); err != nil || len(r.Changes) == 0 {
			t.Errorf("%s: dry run %v, %v", name, r.Changes, err)
		}
	}
	if _, ok := Get("vim"); ok {
		t.Error("unknown agent found")
	}
}
