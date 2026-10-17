package driver

import (
	"os"
	"path/filepath"
	"runtime"
)

// pinned returns exe as installed by agents/install.sh, at the version in
// agents/package-lock.json, or for Muse in agents/muse.json.
func pinned(exe string) (string, bool) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	agents := filepath.Join(filepath.Dir(file), "..", "agents")
	for _, p := range []string{filepath.Join(agents, "node_modules", ".bin", exe), filepath.Join(agents, "bin", exe)} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return filepath.Clean(p), true
		}
	}
	return "", false
}
