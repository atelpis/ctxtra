# Contributing

Bug reports and small fixes are welcome. For a new feature or a larger change, open an issue first, so we
can agree on it before you write the code. ctxtra is in preview and maintained by one person.

Build and test (Go 1.27.3+, macOS or Linux):

```bash
go install ./cmd/ctxtra        # into $(go env GOPATH)/bin
go test ./...                  # unit tests and the CLI scripts in cmd/ctxtra/testdata/script
golangci-lint run ./...        # config in .golangci.yml; also run it in e2e/
```

Each test uses its own temporary `HOME`. The Pi, OpenCode and DSH tests also need `node`, and the Hermes
test needs `python3`.

The CLI tests in `cmd/ctxtra/testdata/script` run the built binary: `setup`, the inspection commands,
and the hook commands that `setup` writes, the way an agent runs them. Run one with
`go test ./cmd/ctxtra -run TestScript/<name>`.

The contract tests keep upgrades free of `ctxtra setup` (see [the spec](docs/spec.md#setup-compatibility)):
`internal/setup/contract_test.go` compares what setup writes with `internal/setup/testdata/contract`, and
`internal/cli/contract_test.go` runs the hooks of every setup in `testdata/clients` that a version wrote.

The agent contract tests run real agents against a mock model and live in their own module:
[e2e/README.md](e2e/README.md). CI runs them on every PR that changes more than Markdown outside `skills/`, `docs/assets/` or `LICENSE`,
so you don't have to install the agents.

[AGENTS.md](AGENTS.md) has the conventions for code, comments, docs and commits. They're the same for
people and coding agents.

Security problems go to [SECURITY.md](SECURITY.md), not to issues.

CodeQL analyzes both Go modules on PRs, pushes to `main` and a weekly schedule. The same workflow audits
the e2e agents' npm lockfile. The lint job runs `govulncheck` for both Go modules.

Go fuzz tests check config parsing, source path containment and invalid hook input. Ordinary `go test`
runs their seed cases. The fuzz workflow explores each target for 10 seconds on PRs and pushes, and two
minutes each night. Run a longer check locally with:

```bash
go test ./internal/config -run '^$' -fuzz '^FuzzConfig$' -fuzztime 2m
```

Keep any failing fuzz input as a regression case under the package's `testdata/fuzz` folder. The workflow
uploads new failing cases as artifacts.

## Layout

- `cmd/ctxtra`: the binary.
- `skills/`: ctxtra's skills, a folder each, named like the skill. `embed.go` embeds them and the docs into
  the binary (`ctxtra skill show`, `ctxtra help`).
- `internal/config`: loads and validates the config and binding files, and matches `match` blocks.
- `internal/resolve`: decides what a session gets and renders the text.
- `internal/chunk`: splits the text into labeled parts for agents that cut a large hook output.
- `internal/agent`: the `Agent` interface. One package per agent (`antigravity`, `claude`, `codex`, `copilot`,
  `cursor`, `devin`, `dsh`, `hermes`, `muse`, `opencode`, `pi`) with its hook setup and answer format; `hookjson`
  edits the JSON hook files they share; `extfile` writes the extension files of Pi, OpenCode, Hermes and DSH;
  `registry` lists them.
  To add an agent, implement `agent.Agent` and add it to the registry.
- `internal/hook`: the entry point the agents run, and the log.
- `internal/setup`: finds the binary and runs each agent's setup.
- `internal/cli`: the commands.
- `internal/source`: fetches pinned sources with git and keeps their accepted commits in the store.
- `internal/trust`: decides whether a file on disk may be read (outside git, no remote, or a trusted source).
- Helpers: `gitrepo`, `fsutil` (bounded, non-blocking reads and atomic writes), `jsonorder`, `paths`,
  `testenv` (tests only).
