# Changelog

Each release says under **Upgrade** whether you need to run `ctxtra setup` after you upgrade. Without
that line, the hooks that an earlier release set up keep working as they are
([setup compatibility](docs/spec.md#setup-compatibility)).

## Unreleased

## v0.1.0

The first release. ctxtra adds your instruction files and skills to coding-agent sessions, chosen by folder
and git remote, without changes to the repositories you work in.

- Works with Claude Code, Codex CLI, GitHub Copilot CLI, Cursor, OpenCode, Pi, Antigravity, Devin, Hermes
  Agent, Muse Code and DeepSeek Harness, on macOS and Linux.
- `ctxtra onboard` sets up the hooks, offers the ctxtra skill, and can import your agents' own
  instruction files.
- Team packages: a git repository pinned to the commit you accepted. `ctxtra source update` shows the diff
  before a new commit reaches your sessions.
- `ctxtra context show` prints what a session gets, and `ctxtra context explain` says why.

Install with `brew install atelpis/tap/ctxtra` or `go install github.com/atelpis/ctxtra/cmd/ctxtra@latest`.
