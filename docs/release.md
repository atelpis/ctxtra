# Release checklist

For a release tag `vX.Y.Z`. The setup contract and its kinds of change are in
[spec.md, Setup compatibility](spec.md#setup-compatibility).

1. Run the checks: `go vet ./...` and `go test ./...` at the root, and the e2e suites. The `e2e` and
   `e2e-latest` workflows run only on `workflow_dispatch` while the repository is private, so start both by
   hand, or run `cd e2e && CTXTRA_E2E_REQUIRE=1 go test -count=1 -timeout 30m ./...` with tmux, once with the
   pinned agents (`e2e/agents/install.sh`) and once with the latest ones (`e2e/agents/install.sh latest`). `TestUpgradeFromRelease` and `TestCodexUpgradeKeepsTrust` must pass:
   they run the setups of earlier releases with this binary.
2. Add a client fixture only when `internal/setup/testdata/contract` changed since the last release
   (`git diff vA.B.C -- internal/setup/testdata/contract`). Then add `testdata/clients/<id>/<agent>` for each
   agent whose files changed, with the files of `internal/setup/testdata/contract/<agent>` and a
   `client.json`. The id is the release: `0.2` for v0.2.0, `0.2.1` for v0.2.1. `TestContract` fails until a
   fixture has the current files. Never change an existing fixture.
3. Add the release to `testdata/clients/releases.json`: the tag, and for each agent the id of the fixture
   with the files that this release writes, for example
   `{"v0.1.0": {"claude": "0.1", "codex": "0.1", "pi": "0.1"}}` with every agent. `TestReleases` checks
   that each fixture exists.
4. Release notes: for each change that needs or allows a new `ctxtra setup` (a capacity or additive
   change), one line: "Run `ctxtra setup --agent <agents>` after this upgrade: <reason>", and "Codex will ask
   you to trust the hooks again" when Codex is one of the agents. Without such a change, the notes say
   nothing about setup: the upgrade doesn't need it.
5. Tag the commit with `vX.Y.Z`.
