# Changelog

## Unreleased

- ctxtra's source repository is now `github.com/ctxtra/ctxtra`. The Homebrew tap stays at
  `atelpis/tap/ctxtra`.

**Upgrade:** If you installed with Go, run `go install github.com/ctxtra/ctxtra/cmd/ctxtra@latest`
once to switch to the new module path. Older binaries may still suggest the old install command.
Homebrew users can keep using `brew upgrade atelpis/tap/ctxtra`.

## v0.4.0

- The session summary groups instructions and skills by source. A file from a source is shown by its path
  in the source, such as `global/style.md`; a file in no source is in the `local` group, by its full path.
  The skills that `ctxtra skill install` adds are in the `ctxtra` group.

- Each group shows up to 30 entries, then `+N more` and a line that points to `ctxtra context files`.
  Before, the limit was 15 for each whole list. The new `summary-message` section of the config hides a
  list (`show-instructions`, `show-skills`) or changes its limit (`max-instructions`, `max-skills`); a limit
  of `0` shows only the number of entries of each source.

## v0.3.0

- Sessions tell you about a new ctxtra release, with the command that upgrades it. Every 4 hours at most, a
  session asks GitHub for the latest release in the background, and the next session shows the result; it
  never waits for the check. This sends your IP address and ctxtra version to GitHub. `check-updates: false`
  in the config turns it off, and `CTXTRA_NO_UPDATE_CHECK=1` stops it in one environment, such as CI.

- A pinned source with `check-updates: true` (or added with `source add --check-updates`) gets the same
  check: `git ls-remote` of its branch, which reads only the commit ID and fetches no files. Sessions
  then say when the branch has new commits, with the `source update` command that reviews them. The check
  uses your git credentials, so leave it off for a source whose ssh key needs a touch or an approval.
  `ctxtra check-updates` runs the checks now, and `doctor` shows what they found or why they failed.

- The "last checked N days ago" and "not checked for new commits on this machine yet" notices are gone.
  Turn on `check-updates` for a source to hear about its new commits. `doctor` says "last fetched"
  instead of "last checked".

- Session notices come after an empty line, each marked with `ℹ`, so they no longer look like part of the
  skills list. The command to run is on its own line, and notices no longer show commit IDs (`doctor`
  still does).

- `source add` and `source update` show content warnings: hidden characters, HTML comments, long encoded
  blocks, `curl | sh` and similar commands, secret paths like `~/.ssh` and `.env`, new URLs, and new
  executable files. `source update` checks only the added lines of the files it shows in full, and shows
  each warning after its line; `source add` checks the files your config uses from the new source.
  `source update --check --json` lists them in `diagnostics`. They only warn, and they don't show that a
  source is safe.

- `ctxtra source check DIR` checks a source repository's binding files in a checkout, the way sessions read
  them from a pinned source: broken YAML and bindings, config-only keys, paths outside the source, missing
  files, unusable skills, and symlinks and submodules that a pinned source leaves out. It reads only DIR,
  runs and fetches nothing, and exits with 1 on an error, so it fits CI. A glob that matches nothing is a
  warning. It doesn't check what the files say. `--json` is supported.

- A `ctxtra://` reference to another source in a pinned source's binding file now says that a binding
  file can't reference other sources, also when you don't have that source.

## v0.2.3

- `doctor` also reads the rotated hook log, `log.jsonl.1`, so it no longer shows "none yet" for runs logged
  before the log was rotated.

- The session-start summary and the size warnings of `explain` and `doctor` show files with the same name by
  full path: from `~` for local files, as `ctxtra://<source>/<path>` for files from a pinned source. Other
  files keep their short name. The count of problems now comes before the dropped files in the summary, so
  long paths can't hide it in Muse.

- Setup and config edits keep only the latest three backups per original file, pruning after a successful
  change. Cleanup errors leave extra copies without failing the config change. An unchanged latest
  backup is reused. New backup names identify the full original path, so files with matching names in different
  folders do not share retention. Import backups are unchanged. Older config backups cannot be safely
  matched to their original paths and are left for manual cleanup; they may contain tokens.

- `ctxtra-follow` tells agents to read bound skills at their listed locations, also when an instruction
  names one, instead of trying their native skill tool. The `ctxtra` skill now covers dropped files in
  troubleshooting and lists `source show` and `source update --check`.

- `doctor` checks every binding, also those that don't match its folder, and exits with 1 when one has a
  missing file or another problem.

## v0.2.2

- `doctor` and the `context` commands exit with 1 when `budget.max-chars` drops a file, as the docs say.
  `context show` also names the dropped files on stderr.

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
