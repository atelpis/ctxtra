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
   empty `## Unreleased`. The release fails without the section.
5. `git tag vX.Y.Z && git push origin vX.Y.Z`. For a release candidate, use `vX.Y.Z-rc.N`: it becomes a
   prerelease and doesn't touch the tap.
6. In the tap, wait for the PR's bottles, then publish them with the tap's publish workflow:
   `gh workflow run publish.yml --repo atelpis/homebrew-tap -f pull_request=N -f head_sha=SHA`, with the PR's
   number and head commit.
7. Check it: `brew update && brew upgrade ctxtra`, `ctxtra version`, `ctxtra doctor`, and one agent session.

## One-time setup

Repository settings, once the repo is public:

- Ruleset for `main`: pull request required, the `ci` and `e2e` checks required, no force pushes or
  deletion.
- Ruleset for tags `v*`: only you can create, update or delete them.
- Releases: turn on immutable releases.
- Security: private vulnerability reporting, Dependabot alerts and security updates, secret scanning with
  push protection, CodeQL default setup.
- About: description, topics, homepage; the social preview from `docs/assets/social-preview.png`.

The Homebrew tap:

1. `brew tap-new atelpis/tap`, then push it to `github.com/atelpis/homebrew-tap`. It comes with the
   workflows that build bottles on PRs and, when you run the publish workflow, publish them. Check the
   publish workflow's file name in `.github/workflows` and use it in step 6 of the release.
2. Add `Formula/ctxtra.rb`, with the `url` and `sha256` of the first release's source tarball
   (`curl -fsSL URL | shasum -a 256`):

   ```ruby
   class Ctxtra < Formula
     desc "Add your instruction files and skills to coding-agent sessions"
     homepage "https://github.com/atelpis/ctxtra"
     url "https://github.com/atelpis/ctxtra/archive/refs/tags/v0.1.0.tar.gz"
     sha256 "..."
     license "MIT"
     head "https://github.com/atelpis/ctxtra.git", branch: "main"

     depends_on "go" => :build

     def install
       system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=v#{version}"), "./cmd/ctxtra"
     end

     def caveats
       <<~EOS
         To add ctxtra's hooks to your agents:
           ctxtra onboard
         Before you uninstall ctxtra, remove them:
           ctxtra setup --remove
       EOS
     end

     test do
       assert_equal "v#{version}", shell_output("#{bin}/ctxtra version").strip
       (testpath/"note.md").write "brew test note"
       system bin/"ctxtra", "binding", "add", "t", "--global", "--instruction", testpath/"note.md"
       assert_match "brew test note", shell_output("#{bin}/ctxtra context show #{testpath}")
     end
   end
   ```

3. Create a fine-grained token with access to `atelpis/homebrew-tap` only, with Contents and Pull requests
   read and write, and save it as the `HOMEBREW_TAP_TOKEN` secret of this repository. Without it, the
   release only warns, and the tap needs the same change by hand.

The formula never runs `ctxtra setup`: setup changes the agents' configs in your home folder, so it stays a
command you run.
