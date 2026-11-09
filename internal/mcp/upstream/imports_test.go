package upstream

import (
	"os/exec"
	"strings"
	"testing"
)

// The proxy, the names and the credentials take servers only as Server:
// the config format is not decided yet, and changing it must not touch
// them. So no package under internal/mcp imports the config layer.
func TestNoConfigImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "github.com/ctxtra/ctxtra/internal/mcp/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		for _, banned := range []string{"/internal/config", "/internal/resolve", "/internal/cli"} {
			if strings.HasSuffix(p, banned) {
				t.Errorf("a package under internal/mcp depends on %s", p)
			}
		}
	}
}
