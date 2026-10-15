package driver

import (
	"os"
	"path/filepath"
	"runtime"
)

// pinned returns exe as installed by agents/install.sh, at the version in
// agents/package-lock.json.
func pinned(exe string) (string, bool) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	p := filepath.Join(filepath.Dir(file), "..", "agents", "node_modules", ".bin", exe)
	if st, err := os.Stat(p); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return "", false
	}
	return filepath.Clean(p), true
}
