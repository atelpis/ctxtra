# Distribution plan

How to ship ctxtra when the repo goes public: install channels, repo preparation, CI changes and the
release process. The repo is also a portfolio project, so it should look like a well-run small OSS
project without padding.

Status, as of 2026-10-23: decided (see [Decisions](#decisions)). The parts in this repo are done; the tap
and the GitHub settings are manual steps in [RELEASING.md](../RELEASING.md).

## Constraints from the code

These come from the current code and docs, and they shape every choice below.

- Hooks store the absolute path of the binary (`setup.Binary` in `internal/setup/setup.go`). It keeps a
  package manager's link in `PATH` unresolved, so `/opt/homebrew/bin/ctxtra` survives upgrades. A channel
  that installs into a versioned path breaks the hooks after each upgrade, until `ctxtra setup` runs again.
- The hook fails open. When the binary is missing, Claude Code, Codex, Muse, Devin and Pi show a message,
  and the other agents show nothing (`docs/spec.md`). A binary that exists but can't run, for example one
  that Gatekeeper blocks because of the quarantine attribute, isn't covered by that message: the session
  starts without the files, with at most a generic hook error. So a channel must never leave a binary that
  macOS refuses to run.
- The hook never uses the network (README). No update check or self-update may be added to it.
- The setup contract starts at 0.1 (`docs/spec.md`, Setup compatibility). `testdata/clients/0.1` is the
  snapshot of what 0.1 writes, and release notes must say when an upgrade needs `ctxtra setup`.
- macOS and Linux only. Windows is built in CI but not supported.
- The binary needs `git` at runtime for git sources, and nothing else. It builds with `CGO_ENABLED=0`.
- `main.version` can be set with `-ldflags`; otherwise the module version from `go install` is used.

## Install channels

### Ship at 0.1

1. **Homebrew, own tap** (`atelpis/homebrew-tap`): a formula that builds from the release's source
   tarball, with bottles built by the tap's CI. See [Homebrew](#homebrew) for why this option.
   `brew install atelpis/tap/ctxtra`.
2. **`go install github.com/atelpis/ctxtra/cmd/ctxtra@latest`**: already works. Needs Go 1.27 or
   `GOTOOLCHAIN=auto`, and `~/go/bin` in `PATH`.
3. **GitHub Releases**: archives for darwin and linux, amd64 and arm64, with `checksums.txt` and build
   provenance attestations. Supported for Linux, for scripts, and as the source for other tools. On macOS,
   an unsigned binary downloaded with a browser gets the quarantine attribute and Gatekeeper blocks it, so
   the README sends macOS users to Homebrew or `go install`. For the archives, the README shows how to
   download with `gh release download` or `curl` (no quarantine), verify with
   `gh attestation verify --repo atelpis/ctxtra`, put the binary in a fixed folder in `PATH` such as
   `~/.local/bin`, and replace it in the same place on upgrade so the hooks keep working.

### Not now

- **npm/npx wrapper, Docker image**: the binary path isn't stable (npx cache) or the binary can't reach
  the user's agent configs (container). Both conflict with how hooks work.
- **`curl | sh` install script**: adds a script to maintain and a security question for users; release
  archives plus Homebrew cover the same people.
- **deb/rpm (nFPM), Nix, Scoop/winget, AUR**: little demand expected for a developer CLI at 0.1; Windows
  isn't supported. Add only when someone asks.
- **mise / aqua / ubi**: they can install from GitHub Releases with no work on our side, but the install
  path may include the version or be a shim. Test whether hooks survive an upgrade before documenting them.
- **homebrew-core**: later. It needs a stable (non-preview) release, an OSI/DFSG license, and Homebrew's
  notability rules. Its benefit: `brew install ctxtra` with no tap and no tap trust step. The tap formula
  is written so it can be submitted to core as is.

## Homebrew

| Option | Verdict |
|---|---|
| Formula in own tap, built from source, with bottles | **Chosen.** Free; macOS and Linux; formula downloads aren't quarantined; same formula can go to homebrew-core. Cost: a second repo with its own CI (`brew tap-new` generates it). |
| Cask from GoReleaser `homebrew_casks`, signed and notarized | Good alternative, with less tap CI: GoReleaser writes the cask from the release archives, so there are no bottles to build. A `binary` cask works on macOS and Linux. Needs the Apple Developer Program ($99/year) for signing and notarization; GoReleaser can do both from a Linux runner. Not accepted in homebrew-core later, which builds formulae from source. |
| Unsigned cask that removes the quarantine attribute in a hook | No. Homebrew 5 deprecated Gatekeeper bypasses and disables failing casks in the official tap; GoReleaser warns that `xattr` may stop working. If quarantine blocks the binary, hooks fail silently (see Constraints). |
| GoReleaser `brews` (formula with prebuilt binaries) | No. Deprecated since GoReleaser v2.10, to be removed in v3. |

Tap details:

- Create with `brew tap-new atelpis/tap`, push to `github.com/atelpis/homebrew-tap`. It includes the
  test-bot workflow that builds bottles on PRs and a publish workflow that uploads them.
- Formula: `depends_on "go" => :build`, `system "go", "build", *std_go_args(ldflags: "-s -w -X
  main.version=#{version}"), "./cmd/ctxtra"`, and a `test do` block that runs `ctxtra version` and
  `ctxtra context show` in a temp HOME. Add `depends_on "git"` only on Linux if needed.
- Tap trust (Homebrew 6): installing by the full name, `brew install atelpis/tap/ctxtra`, trusts only that
  formula, so the README uses the full name and asks for no `brew trust` of the whole tap.
- No `post_install` that runs `ctxtra setup`: setup changes agent configs in the user's home and must be a
  user action (`ctxtra onboard`). A `caveats` block tells the user to run it.

## Before the repo goes public

Required:

- **LICENSE**: MIT (added). Without one, nobody may legally reuse the code, and homebrew-core rejects it.
- **Secret scan of the full history**: `gitleaks git --log-opts="--all"` found no leaks in 76 commits
  (2026-10-23), and every commit uses the GitHub noreply email. Run it again right before going public.
- **Check the e2e recordings** (`e2e/mock/testdata/poc/*.json`) for anything personal: prompts, paths,
  account ids.

Repo settings:

- Description, topics, homepage, social preview (`docs/assets/social-preview.png`).
- Ruleset on `main`: require a PR and the CI checks, block force pushes and deletion.
- Secret scanning with push protection, Dependabot alerts and security updates, private vulnerability
  reporting, CodeQL default setup (no workflow file needed).
- Tag protection ruleset for `v*`, so only the release flow (you) can create release tags.

Files (short; a one-person project with heavy boilerplate looks padded):

- `LICENSE`, `CHANGELOG.md`, `SECURITY.md` (how to report, supported versions: latest only).
- `CONTRIBUTING.md`: how to build, run the unit tests and the e2e tests, and the comment and commit
  conventions.
- `AGENTS.md`: the same dev commands and conventions for coding agents. Fits the project's subject.
- `.github/ISSUE_TEMPLATE/bug.yml`: asks for `ctxtra version`, the agent and its version, and the OS. It
  asks for `ctxtra doctor` output only as the relevant lines, and warns that doctor prints repo remotes,
  file paths and source URLs, which can name private repos, clients or employers: redact them. Security
  issues go to private vulnerability reporting, not issues.
- `RELEASING.md`: the release checklist (see [Release process](#release-process)).

README:

- Install section: Homebrew first, then `go install`, then release archives.
- Uninstall section: `ctxtra setup --remove` first, then remove the binary. Otherwise the hooks point to a
  missing binary.
- Badges: CI, latest release, license, OpenSSF Scorecard.
- A short "How it's tested" section: nine real agent binaries against a mock model, the nightly run against
  their latest releases, and upgrade tests from released setups. It's the strongest engineering signal in
  the repo and is now one sentence.

## CI changes

Existing workflows:

- `ci.yml`, `e2e.yml`, `e2e-latest.yml`: re-enable the commented-out triggers. Standard GitHub-hosted
  runners, macOS included, are free for public repos.
- Add `concurrency: { group: ${{ github.workflow }}-${{ github.ref }}, cancel-in-progress: true }` for PR
  runs.
- No path filters at first. A workflow skipped by `paths-ignore` leaves its required check pending, and
  Markdown files are not docs-only here: the binary embeds `docs/*.md` and `skills/` (`embed.go`).

New `lint` job (in `ci.yml`):

- `go mod tidy -diff` for both modules.
- `golangci-lint` v2 with a committed `.golangci.yml`, gofmt included as its formatter. With the default
  linters it reported about 370 errcheck findings, nearly all writes to the terminal and `Close` calls.
  The config uses the `std-error-handling` preset, also excludes the other output writes and test files,
  and the few errors that production code ignores on purpose are marked `_ =`.
- `govulncheck ./...`. Its first run found a reachable infinite loop in `golang.org/x/text` v0.25.0
  (GO-2026-5970, through the normalization of source file names), fixed by upgrading to v0.43.0, and a
  `crypto/x509` fix in Go 1.27.3, which CI gets with `check-latest`.

New `release.yml`, on push of a `v*` tag:

1. Run the same tests as `ci.yml` (or require that the tagged commit passed CI on `main`).
2. Check that `internal/setup/testdata/contract` matches the newest `testdata/clients/<version>` snapshot,
   ignoring `client.json`. If not, fail: the release changes what setup writes and needs a new snapshot.
3. GoReleaser (`.goreleaser.yaml`): darwin/linux × amd64/arm64, `CGO_ENABLED=0`, `-trimpath`,
   `-X main.version`. Archives include LICENSE and README. `checksums.txt`. Release notes from the
   version's `CHANGELOG.md` section (`--release-notes`). `release.draft: true` and
   `release.prerelease: auto`, so no release assets are public before step 5 and `-rc` tags never become
   "latest". The draft doesn't gate `go install`: a pushed tag is installable at once, so only tag a commit
   that passed CI.
   Reproducible archives are not a goal for 0.1 (it would need normalized file metadata too).
4. `actions/attest` for build provenance of the archives and `checksums.txt`, then
   `gh attestation verify` on one archive in the same job.
5. Publish the draft (`gh release edit --draft=false`). Turn on GitHub immutable releases in the repo
   settings, so published assets and their tag can't change.
6. For non-prerelease tags only: open a PR in the tap that bumps the formula's `url` and `sha256`. The
   tap's CI builds bottles; merge it by hand.
7. Permissions per job: only the release job gets `contents: write`, `id-token: write`,
   `attestations: write`. The token for the tap is a fine-grained PAT or a GitHub App with access to the tap
   repo only, stored as a repo secret. Never a classic PAT.

Supply chain:

- Pin all actions to commit SHAs with the version in a comment; Dependabot keeps updating them.
- Dependabot already waits three days before a version update PR by default (since July 2026), and
  security updates come at once. No `cooldown` setting needed.
- OpenSSF Scorecard workflow, results to code scanning. The README badge needs `publish_results: true` and
  `id-token: write` on that job.
- `goreleaser check` and `goreleaser release --snapshot --clean` on every PR (`release-config` in `ci.yml`).

## Release process

Versions: semver tags `v0.1.0`, `v0.2.0`, … Go modules need the `v` prefix. While 0.x, a minor bump may
change the config format (README says it may); the setup contract still holds from 0.1.

`RELEASING.md` checklist:

1. CI and e2e green on `main`; the latest nightly e2e-latest run green or understood.
2. Manual checks listed in `e2e/README.md` under "checked by hand before a release" (TUI, Codex trust via
   `/hooks`, Cursor with cursorkit, Antigravity app, Hermes host isolation, DSH UI, Devin).
3. If setup output changed since the last release: add `testdata/clients/<version>`, and write in the
   changelog that users should run `ctxtra setup`.
4. `CHANGELOG.md` entry with an "Upgrade" line: "No action needed" or "Run `ctxtra setup`" (and the Codex
   re-trust note when hooks change).
5. Tag `vX.Y.Z` on `main` and push the tag. Merge the tap PR after its bottles are built.
6. Smoke test: `brew upgrade ctxtra`, `ctxtra version`, `ctxtra doctor`, start one agent session.

## Rollout order

1. LICENSE, secret scan, recordings check.
2. Repo files (CHANGELOG, SECURITY, CONTRIBUTING, AGENTS, issue form, RELEASING) and README changes.
3. CI: re-enable triggers, lint job (with the 55 findings fixed), SHA pins, Scorecard.
4. `.goreleaser.yaml` and `release.yml`. While the repo is private, test only
   `goreleaser release --snapshot --clean` (no publishing): artifact attestations need GitHub Enterprise
   Cloud on private repos.
5. Make the repo public and apply the repo settings.
6. Tag `v0.1.0-rc.1`: the full release flow runs, including attestation, and publishes a prerelease without
   touching the tap.
7. Tap repo and formula: a PR in the tap points the formula at the rc's source tarball, and the tap's CI
   builds and tests it. Don't merge it.
8. Fix what failed, tag `v0.1.0`, and merge the tap PR that the release opens.

## Decisions

1. **Homebrew form**: source-built formula in a tap. It's free, and the same formula can go to
   homebrew-core. The alternative was a signed and notarized cask ($99/year, less tap CI, and signed
   binaries for direct macOS downloads too).
2. **License**: MIT. Apache-2.0 would add an explicit patent grant, which matters only when a contributor
   holds a patent on the code.
