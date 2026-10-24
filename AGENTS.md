# AGENTS.md

ctxtra is a Go CLI that adds instruction files and skills to coding-agent sessions through the agents'
hooks. [docs/spec.md](docs/spec.md) is the design, [docs/guide.md](docs/guide.md) the user guide.

## Commands

```bash
go build ./...
go test ./...                         # unit tests and CLI scripts
go test ./cmd/ctxtra -run TestScript/setup_claude   # one CLI script
golangci-lint run ./...               # and in e2e/
cd e2e && go test -count=1 ./...      # agent contract tests, a separate module: see e2e/README.md
```

## Layout

- `cmd/ctxtra`: the binary; `testdata/script/*.txtar` are CLI integration tests (testscript).
- `internal/`: one package per concern (`agent/<name>` per agent, `setup`, `hook`, `config`, `source`, …).
- `docs/` and `skills/` are embedded in the binary (`embed.go`): `ctxtra help` and `ctxtra skill` serve
  them. A change there is a change of the binary.
- `e2e/`: real agents against a mock model.
- `testdata/clients/<release>`: what each release's `ctxtra setup` wrote, for the upgrade tests.

## Rules

- What `ctxtra setup` writes is a contract with released setups (docs/spec.md, Setup compatibility).
  `internal/setup/testdata/contract` holds it. A diff there is a contract change: check it against those
  rules before `go test ./internal/setup -update`.
- The hook never uses the network and never fails a session: on any error it adds nothing.
- Update the docs in the same change as the behavior. Write them in short, plain sentences.
- Comments describe what the code does and why, not how it got there. No comment is better than one that
  restates the code or will go stale.
- Commit messages: one short line, no body.
