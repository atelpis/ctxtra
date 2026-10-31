# Changelog

Each release says under **Upgrade** whether you need to run `ctxtra setup` after you upgrade. Without
that line, the hooks that an earlier release set up keep working as they are
([setup compatibility](docs/spec.md#setup-compatibility)).

## Unreleased

- `doctor` and the `context` commands exit with 1 when `budget.max-chars` drops a file, as the docs say.
  `context show` also names the dropped files on stderr.
- `doctor` checks every binding, also those that don't match its folder, and exits with 1 when one has a
  missing file or another problem.

## v0.2.1

- The list of bound skills tells the agent that its skill tool can't load them, so it reads the `SKILL.md`
  instead. Before, an instruction that named a bound skill could make Claude Code call its Skill tool first
  and get "Unknown skill".

## v0.2.0

- An entry in `instructions` or `skills` that starts with `!` leaves out the paths it matches, for
  example `'!agents/drafts/**'`. ctxtra 0.1.0 reads such an entry as a path that matches nothing and adds
  the files anyway: upgrade before you use it.
- Building from source requires Go 1.27.3 or newer, which fixes a certificate validation vulnerability.
- Release downloads include a signed attestation bundle. [RELEASING.md](RELEASING.md) shows how to verify it.

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
