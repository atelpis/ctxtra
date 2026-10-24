# Contributing

Bug reports and small fixes are welcome. For a new feature or a larger change, open an issue first, so we
can agree on it before you write the code. ctxtra is in preview and maintained by one person.

Build and test (Go 1.27, macOS or Linux):

```bash
go install ./cmd/ctxtra        # into $(go env GOPATH)/bin
go test ./...                  # unit tests and the CLI scripts in cmd/ctxtra/testdata/script
golangci-lint run ./...        # config in .golangci.yml; also run it in e2e/
```

The agent contract tests run real agents against a mock model and live in their own module:
[e2e/README.md](e2e/README.md). CI runs them on every PR, so you don't have to install the agents.

[AGENTS.md](AGENTS.md) has the conventions for code, comments, docs and commits. They're the same for
people and coding agents.

Security problems go to [SECURITY.md](SECURITY.md), not to issues.
