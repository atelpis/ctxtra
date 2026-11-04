package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/testenv"
)

// A binding file over the size limit is checked, not skipped, also when its
// schema comes after a long first line.
func TestCheckFindsLargeBindingFile(t *testing.T) {
	h := testenv.Home(t)
	dir := filepath.Join(h, "team")
	testenv.Write(t, filepath.Join(dir, "ok.yaml"), "schema: ctxtra/v1\nbindings:\n  - name: a\n    match: {global: true}\n    instructions: [a.md]\n")
	testenv.Write(t, filepath.Join(dir, "a.md"), "A")
	big := "# " + strings.Repeat("x", config.MaxFileBytes) + "\nschema: ctxtra/v1\n"
	if err := os.WriteFile(filepath.Join(dir, "big.yaml"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("source", "check", dir)
	if code != 1 || !strings.Contains(out, "error: big.yaml: ") || !strings.Contains(out, "is larger than") {
		t.Errorf("source check: %d %q", code, out)
	}
}

// Each binding file is read with the limits of its own session: users may
// include any one of them.
func TestCheckLimitsPerBindingFile(t *testing.T) {
	h := testenv.Home(t)
	dir := filepath.Join(h, "team")
	testenv.Write(t, filepath.Join(dir, "a.md"), "A")
	pad := "# " + strings.Repeat("x", 3<<20) + "\n"
	for i := range 6 {
		testenv.Write(t, filepath.Join(dir, fmt.Sprintf("b%d.yaml", i)),
			"schema: ctxtra/v1\n"+pad+"bindings:\n  - name: a\n    match: {global: true}\n    instructions: [a.md]\n")
	}
	if code, out, _ := run("source", "check", dir); code != 0 || !strings.Contains(out, "0 errors, 0 warnings") {
		t.Errorf("source check: %d %q", code, out)
	}
}
