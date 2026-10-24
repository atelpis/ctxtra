# Releasing

A `v*` tag runs [release.yml](.github/workflows/release.yml): CI, a check of the setup snapshot, GoReleaser
(archives and checksums in a draft release), provenance attestations, then the release is published. For a
tag without a `-` it then opens a PR in the Homebrew tap. A pushed tag is installable with `go install` at
once, even while the release is a draft, so tag only a commit that passed CI on `main`.

## Each release

1. CI and e2e are green on `main`, and the latest nightly e2e-latest run is green or its failure is
   understood.
2. Do the checks that the tests don't cover: [e2e/README.md](e2e/README.md), "checked by hand before a
   release".
3. If `internal/setup/testdata/contract` changed since the last release, copy it with each agent's
   `client.json` to `testdata/clients/<version>` (for example `0.2`), and say in the changelog that users
   should run `ctxtra setup` (and trust the hooks again in Codex). The release fails without the snapshot.
4. In `CHANGELOG.md`, rename `## Unreleased` to `## vX.Y.Z`, with its **Upgrade** line, and add a new
   empty `## Unreleased`. The release fails without the section. A release candidate uses the section of
   its version: `v0.2.0-rc.1` takes `## v0.2.0`.
5. `git tag vX.Y.Z && git push origin vX.Y.Z`. For a release candidate, use `vX.Y.Z-rc.N`: it becomes a
   prerelease and doesn't touch the tap. Never move or delete a pushed tag: the Go module proxy keeps the
   first one. Fix a bad release with the next patch version.
6. In the tap, wait until every check of the PR has passed, then publish its bottles with the tap's
   publish workflow: `gh workflow run publish.yml --repo atelpis/homebrew-tap -f pull_request=N -f head_sha=SHA`, with the PR's
   number and head commit. It adds the bottles to the formula, pushes to `main` and closes the PR: don't
   merge the PR in GitHub.
7. Check it: `brew update && brew upgrade ctxtra`, `ctxtra version`, `ctxtra doctor`, and one agent session.

## Homebrew tap

release.yml opens the PR in `atelpis/homebrew-tap` with the `HOMEBREW_TAP_TOKEN` secret: a fine-grained
token with Contents and Pull requests write access to the tap only. Without it, the release only warns, and
the formula's `url` and `sha256` need the same change by hand. The formula never runs `ctxtra setup`: setup
changes the agents' configs in your home folder, so it stays a command you run.
