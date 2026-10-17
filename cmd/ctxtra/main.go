// Command ctxtra adds personal and team instruction files and skills to
// coding-agent sessions (Claude Code, Codex CLI, Pi, OpenCode, Cursor Agent CLI,
// GitHub Copilot CLI, Muse Code, Antigravity).
package main

import (
	"os"
	"runtime/debug"

	"github.com/atelpis/ctxtra/internal/cli"
)

// version can be set at build time with -ldflags "-X main.version=...".
var version string

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, buildVersion()))
}

// buildVersion is version, or else the module version that the go command
// records: a tag with go install ...@v1.2.3, a pseudo-version for a commit.
func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
