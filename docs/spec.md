# Config spec: ctxtra/v1

> **Preview.** The format may still change without a migration path. ctxtra does not protect you from the
> content of the files you bind: an instruction file or skill is acted on like anything you type to the
> agent. Bind only files you wrote or reviewed.

For how this is meant to be used, step by step, see [scenarios.md](scenarios.md).

## Terms

- **Session folder:** the folder an agent session starts in.
- **Binding:** an entry of `bindings`, in the config or in a binding file. It says: in sessions that its
  `match` block matches, add these instruction files and these skills.
- **Binding file:** a YAML file with a list of bindings, which the config includes.
- **Config:** the file that declares your sources, has bindings of its own, and includes binding files.
- **Source:** a folder of your own files (trusted), or a git repository pinned to a commit you
  accepted (pinned), declared in the config. Files in it are referenced as `ctxtra://<source>/<path>`.

Instruction files are plain Markdown. Skills are folders with a `SKILL.md`, the same format other agent
tools use. Neither has anything specific to ctxtra.

## Config

`~/.config/ctxtra/config.yaml`, or the file in `$CTXTRA_CONFIG`.

```yaml
schema: ctxtra/v1

sources:
  dotfiles:
    trusted-path: ~/dev/dotfiles      # a repo of your own: read from disk, changes apply right away
  team-ctx:
    git: git@github.com:acme/team-ctx.git
    branch: main                      # where source update looks for new commits
    commit: '9f8e7d6c5b4a39281706f5e4d3c2b1a098765432'   # the accepted version; source update changes it
    check-updates: true               # optional; sessions tell you about new commits (default: false)

bindings:                             # your own bindings, see Binding file below
  - name: me
    match:
      global: true
    instructions:
      - ~/agent-context/me.md          # relative paths are relative to this file's folder

include:                              # binding files, applied after the bindings above, in this order
  - bindings/personal.yaml            # relative to this file's folder
  - ctxtra://dotfiles/ctxtra/go.yaml  # a file in the dotfiles source
  - ctxtra://team-ctx/bindings/billing.yaml

budget:
  max-chars: 40000                    # optional; 0 or missing = no limit

check-updates: false                  # optional; check for new ctxtra releases (default: true)
summary-message:                      # optional; what the session summary lists
  show-skills: false                  # default: true; also show-instructions
  max-instructions: 10                # entries of each source (default: 30, 0 = only their number); also max-skills
```

Every key except `schema` is optional. A config with only `schema` and `bindings` is enough for your own
files.

`ctxtra binding add NAME (--global | {--dir PATTERN | --repo PATTERN}...) {--instruction FILE | --skill DIR}... [--dry-run]`
adds a binding to the config's own `bindings`, after the others. Paths are relative to the current folder
and are written from `~` when they are under it; `ctxtra://` references are written as they are. The config
is changed only when a session that the binding matches would get every file and skill in it: none is
missing, each is trusted, and no other problem is new. `--dry-run` does the same checks and changes
nothing. `ctxtra binding remove NAME` removes one of the config's own bindings, and `ctxtra binding list`
lists every binding with the file it is in. A binding in a binding file, and a change to an existing
binding, are edits by hand.

`ctxtra binding-file add (FILE | ctxtra://SOURCE/PATH)` adds a binding file to `include`: a file on your
disk in a trusted place, or a file of a source you have. It never adds, pins or trusts a source; for a file
in a repository that isn't a source, it names the `ctxtra source add` commands that would add it.
`ctxtra binding-file remove ENTRY` removes one entry, and `ctxtra binding-file list` lists them.

## Sources and trust

ctxtra reads a file from disk only if the file is yours. A file counts as yours when it is:

- outside git,
- in a git repository with no remote, or
- under a `trusted-path` source.

A file in a repository that has a remote and isn't a trusted source is skipped, with a problem that says
how to trust the repository. Its content may come from other people, and a binding file there could add
any file on your disk to the session. This covers binding files, instruction files and `SKILL.md` files.
The config file itself is always read.

- `trusted-path` must be absolute or start with `~`. It is usually the top of a repository, but can be a
  folder inside one: then only the files under that folder are trusted.
- It trusts one repository. A repository nested below it (a submodule, a clone in a subfolder) needs its
  own source, unless it has no remote. A file is read only if every repository around it is trusted.
- Folders are compared as folders, not as text: `~/dev/dotfiles` doesn't cover `~/dev/dotfiles-evil`, and
  a symlink to the folder or another spelling of it on a case-insensitive disk is the same folder. A second
  clone or a worktree in another folder is a different folder: add it as its own source if you want it.
- Symlinks are followed only when every symlink on the way, and the file it leads to, pass the same check.
  A link in an untrusted repository can't lead to your files, and a link in your files can't lead into an
  untrusted repository.
- ctxtra finds repositories by reading `.git` and the repository's git config, without running git.
  `[include]` and `[includeIf]` in a repository's config aren't supported. A repository it can't read counts
  as untrusted.

A source has a name (letters, digits, `.`, `_`, `-`) and exactly one kind: `trusted-path`, `git` or
`local-git`.

`ctxtra://<source>/<path>` works in `include`, and in `instructions` and `skills` of any binding. The path as
written must stay inside the source: `..` that leaves it, and absolute paths, are errors. For a trusted
source it is the same as the path under the source's folder. A symlink there is followed by the rules
above, so it can lead outside the source, but only to a file that is trusted on its own.

## Pinned sources

A `git` source is a git repository, usually one that other people push to. ctxtra keeps its own copy, so you don't need a clone, and serves one
commit of it: the one in `commit`. A new commit on the branch reaches your sessions only after you review
and accept it with `ctxtra source update`, or after you change `commit` yourself.

```text
$ ctxtra source add git git@github.com:acme/team-ctx.git --include bindings/billing.yaml
fetched team-ctx: main at abc1234 (git@github.com:acme/team-ctx.git)
added source team-ctx (pinned: new commits apply after you accept them in ctxtra source update)
included ctxtra://team-ctx/bindings/billing.yaml
  binding billing: for github.com/acme/billing
    instructions: services-overview.md
```

- `git` is an `https://` or `ssh://` URL, or `user@host:path`. Other transports and local paths are errors.
- `branch` is required. `source add git` uses the remote's default branch unless you pass `--branch`.
- `commit` is a full commit ID in quotes: 40 hex digits, or 64 for SHA-256 repositories. A source without
  `commit` is inactive until you accept its first commit with `ctxtra source update <name>` in a terminal;
  `--check` shows that commit without accepting it.
- **The first version is accepted without review**, the same decision as cloning the repository: read a
  source repository before you add it. `source add git` and `source add local-git` show its bindings, its
  executable files, and [content warnings](#content-warnings) for the files your config uses from it.
- Accepting a commit accepts **every file in it**, also files the review lists only by name: a file you
  reference later is used at once, without another review.
- Anyone who can edit your config can choose the version, for example through a dotfiles repository that
  others push to. A `commit` copied from another laptop was reviewed there.
- A binding file in a pinned source may only reference files in the same source: relative paths, or
  `ctxtra://` with its own name. Absolute paths, `~`, `..` that leaves the source and other sources are
  errors, and the binding is skipped. A source repository can't add your files to a session, and can't
  declare sources or include other files: only your config does.
- Files are read from ctxtra's store (below), never from a clone. Symlinks and submodules in the
  repository are left out.
- Sessions never fetch. With `check-updates: true`, they tell you when the branch has new commits (see
  [Update checks](#update-checks)); without it, run `ctxtra source update` regularly.

### Commands

| Command | Does |
|---|---|
| `ctxtra source add git URL [--branch BRANCH]` | Fetches the branch and adds the source with its current commit. |
| `ctxtra source add git DIR [--remote REMOTE] [--branch BRANCH]` | Adds the repository that DIR is a clone of (below). |
| `ctxtra source add local-git DIR` | Adds your clone as a `local-git` source (below). |
| `ctxtra source add trusted-path DIR` | Adds a folder as a `trusted-path` source. |
| `ctxtra source list`, `ctxtra source show NAME` | The sources and their state; one source with its binding files and clones. |
| `ctxtra source update [--full] [NAME]...` | For each pinned source: makes sure the accepted commit is in the store, fetches the branch, shows what changed (below), and on `y` writes the new commit to the config. Only in a terminal. |
| `ctxtra source update --check [--json] [NAME]...` | The same fetch and review, without accepting or dismissing anything. Works anywhere. |
| `ctxtra source restore [NAME]...` | Fetches exactly the commits in the config, and changes nothing in it. Use it on a new laptop, or when the store is gone. It fails when a commit can't be fetched (removed by a force-push, or never pushed). |
| `ctxtra source trust NAME [DIR]` | Converts a pinned source to `trusted-path` in place (below). |
| `ctxtra source remove NAME` | Removes the source and its `include` entries, and lists the lines of your config and binding files that still use it. |
| `ctxtra source check DIR [FILE]...` | Checks a source repository's binding files in a checkout, for maintainers and CI (below). |

Every `source add` takes `--name NAME` (default: the repository or folder name), `--include PATH` to add
binding files of the source to `include` in the same edit (repeatable), and `--dry-run`, which checks and
fetches nothing. Without `--include`, it lists the binding files in the source. `source add git` and
`source add local-git` also take `--check-updates`, which writes `check-updates: true` for the new source;
without it, they say how to turn the checks on.

### A clone you already have

```text
$ ctxtra source add git ~/dev/team-ctx --include bindings/billing.yaml
~/dev/team-ctx is a clone of git@github.com:acme/team-ctx.git, added as source team-ctx (pinned)
starts at your local commit 9f8e7d6; later updates follow origin/main (remote not checked yet)
included ctxtra://team-ctx/bindings/billing.yaml
```

- The source is a `git` source with the clone's remote and the upstream of its current branch, starting at
  the clone's committed `HEAD`. Uncommitted changes are not used. Later, `source update` follows the remote
  branch, not the clone. A clone with several remotes, or without an
  upstream, needs `--remote` or `--branch`.
- Paths under the clone in your own bindings keep working, and are served from the source:
  `~/dev/team-ctx/jira-ticket.md` is `ctxtra://team-ctx/jira-ticket.md`, also after the clone moves or is
  deleted. `doctor` suggests the `ctxtra://` form. This mapping is kept in `~/.ctxtra/mappings.json`, by
  the source's name and URL; it is never guessed from a matching remote. A `trusted-path` source that covers
  the folder comes first.
- A missing file under the clone is skipped, not read from disk. When the source is removed, renamed or its
  URL changes, paths under the clone are skipped with `not in the config any more`, not read from disk.
  `ctxtra source remove` lists them.
- If `mappings.json` is deleted, paths under old clones are ordinary paths again: checked as usual, and
  skipped in a repository with a remote. If it is broken, ctxtra can't tell which paths were mapped: every
  plain path outside your `trusted-path` sources is skipped, and ctxtra doesn't write the file, until you
  fix or delete it.
- Reading the clone never fetches: git runs without transports and without lazy fetching.
- If the repository is yours (your dotfiles), `ctxtra source trust NAME` converts the source to
  `trusted-path` under the same name, reading the clone: its `ctxtra://` references are read from disk from
  then on. DIR can be left out when ctxtra knows exactly one clone of the source. Other clones mapped to it
  stay mapped, and their paths stay skipped. `ctxtra source add trusted-path DIR --name OTHER` adds a
  separate trusted source instead, and the pinned one stays: paths under the clone are then live,
  `ctxtra://` references to the pinned source stay pinned. Use this to edit a source repository and see
  your uncommitted changes.

### Your clone, pinned

```yaml
sources:
  team-ctx-dev:
    local-git: ~/dev/team-ctx
    commit: '1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d'
```

A `local-git` source is pinned like a `git` source, but `source update` follows the clone's committed `HEAD`
(detached works too) instead of a remote: for trying your commits before you push them. ctxtra fetches the
commit from the clone; it never copies the clone's working tree, index, config or hooks. It adds no mapping:
reference its files as `ctxtra://team-ctx-dev/<path>`.

### Check a source repository

```text
$ ctxtra source check .
checked ~/dev/team-ctx as the source team-ctx
  bindings/billing.yaml: 2 bindings
error: billing (bindings/billing.yaml): missing: instructions/security.md
warning: bindings/billing.yaml: line 9: instructions entry "drafts/*.md" (in "drafts"): glob matches no instructions
1 error, 1 warning
```

`source check` reads the binding files in DIR, the top of the repository, the way sessions read them from
a pinned source, with DIR in place of the accepted commit. It reports what someone who includes them would
find broken:

- Errors: a binding file that is skipped as a whole (broken YAML, a wrong `schema`, a key other than
  `schema` and `bindings`, `sources`, `include` or `budget`); a binding that is skipped (an unknown key,
  a broken `match` or pattern, a path outside the source, a reference to another source); a missing
  instruction file or skill folder; a `SKILL.md` without frontmatter or `description`; two skills with the
  same `name` in one binding; a path that a pinned source doesn't have: in `.git`, through a symlink
  (also a glob's folder), or in a nested repository or submodule.
- Warnings, after which the binding still applies: a glob that matches nothing, an exclusion that leaves
  nothing out, and a `ctxtra://` reference to the source itself, which works only when users give the
  source that name. A relative path works under any name.

Without FILE, it checks every `.yaml` and `.yml` file in DIR with a top-level `schema: ctxtra/...` line,
outside symlinks, `.git` and nested repositories; a YAML file it can't read is an error. FILE limits the
check to those binding files, relative to DIR: use it when DIR has a YAML file with that line that isn't a
binding file, an example config for instance.

- `--name NAME` is the source name that `ctxtra://NAME/` references use. The default is DIR's folder name.
  Every other source is an error, as in a pinned source.
- It reads only DIR: not your config, sources, store or clones. It runs no git and fetches nothing, and
  needs no trust: DIR can be a clone with a remote.
- It reads the files on disk. An uncommitted or ignored file passes here, and is missing for others; in CI
  the checkout is the commit.
- Each binding file is read, and each binding checked as if it matched a session, with the limits of a
  session of its own: users may include any one of them. Globs skip what a pinned source leaves out.
- It checks that the files are usable, not what they say. It doesn't find unsafe instructions, prompt
  injection, or risky skill scripts. It doesn't know which agents, budget or other bindings a user has, so
  it says nothing about whether the text fits.
- Exit codes: 0 without errors, also with warnings; 1 with an error, or when DIR has no binding files; 2
  for a usage error. `--json` prints the result and every error and warning as `diagnostics`.

In CI, for example GitHub Actions:

```yaml
steps:
  - uses: actions/checkout@v4
  - uses: actions/setup-go@v5
    with:
      go-version: stable
  - run: go install github.com/ctxtra/ctxtra/cmd/ctxtra@latest   # or a fixed version
  - run: ctxtra source check . --name team-ctx                     # the name users give the source
```

### Review

```text
$ ctxtra source update
team-ctx: abc1234 -> def5678 (main, git@github.com:acme/team-ctx.git)
warning: binding billing in bindings/billing.yaml now applies to every session (was: github.com/acme/billing)
diff --git a/bindings/billing.yaml b/bindings/billing.yaml
...
+Install the CLI: curl -fsSL https://get.example.com/cli.sh | sh
warning: line 14: remote code: curl -fsSL https://get.example.com/cli.sh | sh
warning: line 14: new URL: https://get.example.com/cli.sh
also changed, not used by you:
  changed  README.md
  added    tools/setup.sh  executable
Accepting accepts these files too: a file you reference later is used without review. ctxtra source update --full shows them, with content warnings.
Accept? [y/N]
```

- Shown in full: the files your config uses from the source with the accepted commit or with the new one,
  under any `match`. That is the binding files in `include`, every file and skill they reference, the
  source's files and skills in your own bindings, and every file of a used skill. A reference that the new
  commit removes shows too.
- Every other changed file is listed by name with the kind of change: added, removed, changed, or mode.
  `--full` shows them in full too.
- A warning for every binding in the source's binding files whose `match` changes, and for every new one:
  `match` decides which sessions get the files.
- Errors in the source's binding files that the new commit brings, for example a path outside the source.
- Content warnings (below) for the lines that the files shown in full add, each after its line.
- The new commit is written to the store before the review. A commit over the limits fails there.

`source update` asks only when stdin and stdout are a terminal, so you see what you accept; the question
goes to stderr. Without a terminal, or with `--no-input`, it fails before it fetches, and `--check` does
the fetch and the review without asking. There is no option that accepts without asking.

When you answer `N`, ctxtra remembers the commit as dismissed. Sessions that use the source and
`context files` say that the source has a newer commit you didn't accept, and `doctor` says
`def5678 not accepted (dismissed)`. The next `source update` shows the review again. Input that ends without an answer decides nothing: no commit is accepted or dismissed.

### Content warnings

`source add` and `source update` point out text that is easy to miss when you read a diff. They only warn:
nothing is blocked, and a file can have a good reason for any of them. A security guide can show `curl | sh`,
for example.

| Warning | What it finds |
|---|---|
| `hidden character` | Characters you can't see: zero-width characters, text-direction marks, invisible tag characters, line separators, other Unicode format characters, and control characters. Also two or more variation selectors in a row. A tab, a carriage return at the end of a line, a byte order mark at the start of a file and a zero-width joiner in an emoji are not reported. |
| `HTML comment` | Text in `<!--` and `-->`: the agent reads it, but a rendered Markdown view hides it. A comment is reported at its first changed line; comments that share a line share the warning. |
| `encoded text` | 80 or more hex digits in a row, or 80 or more base64 characters (`A-Z`, `a-z`, `0-9`, `+`, `/`, `-`, `_`) in a row with an uppercase letter, a lowercase letter and a digit. Text in URLs doesn't count. |
| `remote code` | A download command (`curl`, `wget`, PowerShell's `iwr`, `irm`, `Invoke-WebRequest`, `Invoke-RestMethod`) whose output a shell or an interpreter runs: through a pipe (`curl ... \| sh`, also with `sudo`, `env` or a path like `/bin/sh`), after `&&`, inside `$( )`, `<( )` or backticks, or `iex (iwr ...)`. |
| `secret path` | Files and folders that hold keys or tokens: `.ssh`, `.gnupg`, `.aws`, `.azure`, `.kube`, `.config/gcloud`, `.docker/config.json`, `.netrc`, `.npmrc`, `.pypirc`, `.git-credentials`, `.env` and `.env.*`, and the key files `id_rsa`, `id_dsa`, `id_ecdsa` and `id_ed25519`. |
| `new URL` | An `http`, `https`, `ftp`, `ws` or `wss` URL that none of the files you use or the review shows had in the accepted commit. Each URL is reported once. In a first version, every URL is new. |
| `new executable file` | A file shown in full that the commit adds as executable or makes executable. A file listed by name says `executable` instead. |

- `source update` checks only the lines that the files shown in full add, and shows each warning after its
  line: `warning: line 14: remote code: ...`. It reads the whole new file to know where a comment starts.
- Files listed by name are not checked. `--full` shows and checks them.
- `source add git` and `source add local-git` check every line of the files your config uses from the new
  source: with `--include`, the binding files and every file and skill they reference. Without
  `--include`, the config uses nothing from it yet, and nothing is checked. A file you reference later is
  not checked.
- A file that isn't UTF-8 text, or has a NUL byte, is not checked, and the review says so:
  `note: not checked for content warnings: binary or not UTF-8`.
- At most 50 warnings about a file's text are shown. A file with more gets a note with their number. A URL
  is new again in the next file when its warning wasn't shown.
- `source update --check --json` lists them in `diagnostics`: `warning` with `SOURCE: PATH:LINE: CHECK: TEXT`,
  or `SOURCE: PATH: new executable file`, and `notice` for a file that isn't checked or has more than 50.
- Text from the files is shown with control and format characters escaped, as in the diff.
- The checks are fixed patterns. They use no network and no model: the same commits give the same warnings.

The warnings help you read. They can't show that a source is safe: plain text can tell an agent to do harm
without any of these patterns, and a pattern can be written so that it doesn't match. No warning only means
that these patterns weren't found. Read what you accept.

### Notices

A session that uses a pinned source with `check-updates: true` says when the last
[update check](#update-checks) found new commits. It also says when you dismissed a commit:

```text
ℹ team-ctx has new commits on main
  run `ctxtra source update team-ctx` to review them.
ℹ team-ctx has a newer commit that you didn't accept
  run `ctxtra source update team-ctx` to review it again.
```

Claude Code, Codex, Muse and Pi show these notices at session start, after an empty line, each with `ℹ` and
the command to run on its own line. Notices name no commit IDs; `doctor` does. `context files` and
`doctor` show the notices too, with the command on the same line. Without `check-updates`, there is no notice about new
commits: run `ctxtra source update` when you want to look.

When a new commit doesn't continue the accepted one, `source update` warns: the branch was force-pushed.
Accepting it is still your choice.

The commands that change the config change it as text: they replace the `commit` value or add lines, and keep your
comments and layout. In a flow-style map (`{...}`) they write the file again from its YAML, which keeps
comments but not the layout. A changed config is checked before it is written, written atomically, and the
old one is kept in `~/.ctxtra/backups`. Setup and config edits keep the latest three backups per original
file, pruning only after the config is successfully replaced or removed. Cleanup is best effort: a
cleanup error can leave extra copies, but does not turn a completed config change into a failed one.
If the latest backup has the same content, it is reused. New backup names contain a SHA-256 hash of the
absolute original path, a short readable name and a UTC nanosecond timestamp. Parent-directory symlinks
are resolved before hashing. Timestamps advance even if the clock moves backward; names break ties.
A symlinked config stays a symlink, and its target path identifies its backups. Import backups and
unrelated files are not pruned. Older config backups lack the full-path identity and are left alone,
because their names can refer to several original paths. Remove them manually when no longer needed;
agent config backups may contain tokens. If the source changed in the config while `source update`
waited for your answer, nothing is accepted.

### Store

- `~/.ctxtra/git/<id>.git`: a bare repository per source.
- `~/.ctxtra/store/<id>/<commit>/`: the files of each accepted commit, and of each commit `source update` showed you,
  private and read-only. Sessions
  read only these, and only through `ctxtra://`: a plain path into the store is an error, because a
  pinned source's binding file read that way could reference any file. A missing commit is reported
  (`run: ctxtra source restore NAME`), and the references to it are skipped; the rest of the binding still
  applies.
- `<id>` comes from the URL: a source whose URL changes doesn't use the old data.
- A commit is deleted from the store 30 days after the last session that used it started (`source update`
  cleans up). The accepted commits are never deleted: of this config, and of every config that a ctxtra
  command changed (with `$CTXTRA_CONFIG`, for example), listed in `~/.ctxtra/configs.json`. If one of those, or that
  list, can't be read, nothing is deleted. A session marks a commit as used before it reads from it; while
  cleaning decides about that commit, the session waits up to a second, and else skips the source's files
  for this session. A session that runs longer than 30 days on an old commit can lose its skill files.
- `~/.ctxtra/state.json`: per source, the last fetch, the dismissed commit and what the update checks found;
  what the release check found. Losing it loses the notices.
- `~/.ctxtra/mappings.json`: the folders of clones added as git sources (above). `~/.ctxtra/configs.json`:
  the config files that ctxtra commands changed.

### How ctxtra runs git

- It runs your `git`, so ssh keys, the ssh agent and credential helpers work. Only `source add`,
  `source update` and `source restore` fetch, and git may prompt there. Sessions never fetch. The
  [update checks](#update-checks) run `git ls-remote`, which reads only the branch's commit ID, and git may
  not prompt.
- Only the https and ssh transports, and the file transport only to fetch from a clone you named (an
  clone added as a git source, a `local-git` source). No hooks, no submodules, no automatic maintenance. Nothing is checked
  out, so `.gitattributes` filters (git-lfs, smudge) never run. Diffs use no external diff and no textconv.
- Environment variables that point git at another repository or config are cleared.
- Every file path in a commit is checked before anything is written: no `..`, no `.git`, no control
  characters, no two file or folder names that a case-insensitive or Unicode-normalizing disk would merge.
  A commit over the limits (10,000 entries, 10 MiB per file, 100 MiB in total) is refused as a whole. A
  repository larger than 1 GiB with its history is refused too, and git's output is limited.
- Control characters in diffs, paths and messages are shown as escapes, so they can't hide lines in your
  terminal.

## Update checks

ctxtra can tell you about a new ctxtra release, and about new commits of a pinned source. A check reads
only a version number or a commit ID. It fetches no files, changes nothing in the config, and accepts
nothing: you still upgrade ctxtra and review commits yourself.

| Check | Default | Turn it on or off | What it asks |
|---|---|---|---|
| New ctxtra release | on | `check-updates: false` at the top of the config | GitHub's API, for the latest release (prereleases left out) |
| New commits of a source | off | `check-updates: true` on the source | the source's remote, with `git ls-remote` of its branch; a `local-git` source, the clone's `HEAD` |

Sessions never wait for a check. At session start, the hook shows what earlier checks found, and starts a
check in the background when the last one is older than 4 hours. The background check runs as a separate
process, `ctxtra check-updates`, which checks only what was due, and the next session shows its result.
Parallel sessions start one check: the first one records the attempt. A failed check also waits 4 hours
before the next try.

The notices go only to you, never to the model, in the agents that show a message at session start (see
[Notices](#notices)):

```text
ℹ ctxtra update v0.3.0 is available - https://github.com/ctxtra/ctxtra/releases
  run `brew upgrade atelpis/tap/ctxtra` to update.
ℹ team-ctx has new commits on main
  run `ctxtra source update team-ctx` to review them.
```

- The release notice comes first. It compares with the version of the ctxtra that runs, so it goes away
  after an upgrade. Its second line, the upgrade command, is there only when ctxtra was installed with
  Homebrew or `go install`. Development builds (`dev`, or a version from a commit) aren't checked at session start.
- The source notice compares with the accepted commit. A commit you dismissed is no new notice.
- A failed check adds no notice and never counts as up to date: the notice from the last check that worked
  stays. `doctor` shows why the check failed, or couldn't start, and when the last one that worked ran and
  what it found. For a pinned source without `check-updates`, `doctor` says that its update checks
  are off.
- `ctxtra check-updates` runs the checks now and shows their results. It exits with 1 when a check fails,
  or when it can't save the results for sessions.
- `CTXTRA_NO_UPDATE_CHECK=1` stops the checks that sessions start, for CI and restricted networks.
  Notices from earlier checks still show.
- Only the config can turn checks on or off: a binding file can't have `check-updates`. A config that is
  ignored as a whole (see [Validation](#validation)) runs no checks, because its `check-updates: false`
  may be what failed.

What a check sends:

- The release check sends your IP address and ctxtra version (in the User-Agent) to GitHub.
- A source check connects to the source's remote with your git credentials, like `source update`. git
  may not prompt there: a key with a passphrase that isn't in your ssh agent fails the check. But an ssh
  agent that asks for approval (for example 1Password) or a hardware key can still ask you to approve or
  touch it when a session starts. Leave `check-updates` off for such a source.

## Binding file

```yaml
schema: ctxtra/v1
bindings:
  - name: soul                        # unique in this file
    match:
      global: true                    # every session
    instructions:
      - ~/agent-context/me.md
  - name: work
    match:                            # matches if ANY entry matches
      dirs:
        - ~/dev/work/**
      repos:
        - github.com/acme/**          # a remote pattern
        - remote: github.com/acme/mono
          subpath: services/billing/** # remote AND subpath
    instructions:
      - ../agents/work.md              # relative to this binding file's folder
    skills:
      - ~/skills/deploy               # a folder with a SKILL.md
```

A binding file has only `schema` and `bindings`. `sources`, `include` and `budget` work only in the
config: in a binding file they are errors, and the whole file is skipped. Bindings in the config itself
have the same keys.

## Match

`match` needs at least one key. The binding applies if **any** entry of **any** key matches. There is no
default: a binding without `match` is an error, so a missing key never makes it apply to every session.

| Key | Matches when |
|---|---|
| `global: true` | Always. `false` is an error: remove the key instead. |
| `dirs` | A pattern matches the session folder, or, inside git, the repository's main checkout. The second case makes worktrees match wherever they are on disk. Each pattern is absolute or starts with `~`. Symlinks in the fixed part of a pattern are resolved. |
| `repos` | The session is in a git repository with a remote that matches the entry. An entry is a remote pattern, or a map with `remote` and `subpath`: then, also, the session folder relative to the worktree top must match `subpath` (`.` at the top). |

Patterns use [doublestar](https://github.com/bmatcuk/doublestar) globs: `*` stays inside one path segment,
`**` matches any number of segments, `x/**` also matches `x`, and `{a,b}` works. A pattern without `/**`
matches only that exact folder.

For a submodule, the main checkout is the submodule's folder. For a repository made with
`git init --separate-git-dir`, git doesn't record where the main checkout is, so `dirs` matches only the
session folder there.

If git doesn't answer within 3 seconds, the session counts as outside git: `repos` don't match, and every
other binding still applies.

Remotes are compared in a short, lower-case form: `git@github.com:Acme/Svc.git` and
`https://github.com/acme/svc` are both `github.com/acme/svc`. A repository with several remotes matches if
any of them matches.

## Instructions and skills

- `instructions` and `skills` are lists. A binding needs at least one of them.
- Each path is absolute, starts with `~`, is relative to the folder of the file the binding is in (the
  config or a binding file), or is a `ctxtra://<source>/<path>` reference.
- `instructions` and `skills` support the same doublestar globs as `match`. Quote patterns in YAML
  and on the command line, for example `instructions: ['agents/**/*.md']` or
  `ctxtra binding add mine --global --instruction '~/agent-context/**/*.md'`. Skill patterns select folders containing a regular `SKILL.md`, so `skills/**` can select skills
  recursively without selecting their parent folders.
- Glob metacharacters in a path are interpreted as patterns; for literal braces use character classes,
  for example `agents/[{]draft[}].md` to select `agents/{draft}.md`. Metacharacters in a resolved parent
  folder (such as the home or config folder) remain literal.
- Entries apply in list order; each pattern expands in lexical path order. Overlapping entries are
  deduplicated at their first position. A pattern with no matches is reported and skipped.
- An entry that starts with `!` leaves out the paths it matches: `instructions: ['agents/**/*.md',
  '!agents/drafts/**']`. It removes what the other entries of the same list add, wherever it is in the
  list. It is a path or a pattern, resolved like the other entries, and is compared with the added paths
  as text: symlinks are not followed. When `~/alias` links to `~/notes`, `!~/alias/x.md` does not leave
  out `~/notes/x.md`. Quote it: YAML reads an unquoted `!` as a tag. For a relative path whose name starts
  with `!`, write `./!name`.
- An exclusion that leaves nothing out is reported, and the binding still applies. A list of only
  exclusions, and an exclusion that can't be resolved (an unknown source, a source that isn't available,
  a path outside the pinned source), are errors, and the binding is skipped: without the exclusion, it
  would add more than intended. `context explain` lists the paths that exclusions left out.
- Glob expansion skips symlink entries and does not descend into symlinked folders. Symlinks in the
  fixed prefix retain the usual trust checks. Every local file still needs to be trusted.
- Each pattern is limited to 10,000 directory entries and operations, with a maximum traversal depth of
  64. A traversal error or exceeded limit skips the whole pattern, with a problem.
- Pinned-source patterns expand only against the selected commit. `source update` reviews matching files from
  both the old and proposed commits, including newly matched files. Local patterns are expanded live:
  new matching files apply immediately. Use `context files`, `context show` or `context explain` to inspect the result.
- Each file is added once per session, at its first position, and the same for each skill. Two skills with
  the same `name`: the first is used, the other is reported.
- `SKILL.md` must start with YAML frontmatter that has a `description`. `name` is optional (default: the
  folder name). Other keys are ignored.
- Claude Code, Codex, Muse, Cursor, Copilot, Antigravity, Hermes, DSH and Devin get the file contents and a list of the skills (name, description, path),
  and read a `SKILL.md` when a task needs it. The list says that these skills are not the agent's own: its skill
  tool and slash commands can't load them. Pi gets instruction files as context files and skills as native skills.
  OpenCode gets instruction files in the system prompt and skills as native skills, which its `skill` tool loads.

## Order and budget

Bindings apply in config order: the config's own `bindings`, then the bindings of each file in `include`,
in order.

With `budget.max-chars`, whole files are dropped from the end until the added text fits. The budget counts
characters (Unicode code points), not bytes, and covers the whole added text, headers included. The skill
list is never dropped. `ctxtra context explain` shows what was dropped.

## Capacity

Each agent gets a limited amount of text in full, its capacity. Above it, whole files are dropped from the
end, the same way as with `budget.max-chars`; with both, the lower limit applies. The skill list is never
dropped. The message at session start (in the agents that show one), the log, `context explain` and `doctor` name
the dropped files.

| Agent | Capacity | How the text gets there |
|---|---|---|
| Claude Code | 360,000 characters, counted in UTF-16 | 40 hooks per event, a part of up to 9,000 each |
| Muse | 360,000 characters, counted in UTF-16 | 40 hooks per event, a part of up to 13,000 bytes each, counted as in the JSON answer, and a summary of up to 2,000; text that isn't ASCII, or has many quotes or backslashes, needs more parts, so fewer files fit |
| Codex | 1,000,000 bytes | one hook with `additionalContextLimit` (250,000 tokens of 4 bytes); Codex 0.145.0 or later |
| Hermes | 1,000,000 characters | a plugin callback per part of up to 9,000 characters, or `hooks.output_spill.max_chars` if lower; the capacity shrinks with it |
| Antigravity | 650,000 bytes | one hook, an injected step per part of up to 40,000 bytes |
| Copilot | 140,000 bytes | one hook |
| Pi, OpenCode, DSH | 1,000,000 characters | one plugin |
| Cursor | 1,000,000 characters | one hook; not measured in full: in the Agent CLI every sampled codeword of 800,000 characters arrived, the desktop app doesn't wait for the hook (see [the guide](guide.md#limits)) |
| Devin | 1,000,000 characters | one hook; not measured |

Characters are Unicode code points; Claude Code and Muse count UTF-16 units, where an emoji or another
character outside the Basic Multilingual Plane counts 2. Copilot counts tokens, and ctxtra counts bytes of
UTF-8 for it: a tokenizer that works on bytes makes at most one token of a byte, so bytes are an upper bound
on the tokens of any text. Copilot passed about 150,000 tokens, and 140,000 bytes leave room for its own
prompt. The bound costs English text, about 140,000 characters fit where about 600,000 would, but no
estimate per character holds for every text: Copilot dropped all of 194,000 characters of short words and
punctuation (`x!x!`). Non-Latin text counts more in the agents that count bytes: a CJK character is 3
bytes in Codex, Antigravity and Copilot. Copilot drops all of the text, without a word, when it doesn't fit
the model's context window; ctxtra drops files before that.
When Copilot compacts a long conversation, ctxtra's text is gone for the rest of the session: Copilot
doesn't run the hook again.

Text that fits one part is given as it is. Otherwise each part starts with a label, `[ctxtra part 3 of 12]`,
and the first one says that the parts are one text, in part order. A file that continues from the part
before starts with `Contents of PATH, continued (part 2 of 4):`. A part ends before a file when little room
is left, else at a line end; a line longer than a part is split between characters.

Claude Code and Muse run the hooks of an event at once. The first one resolves the session and writes the
parts to `~/.ctxtra/slots/`, readable by you only; the others read them there, so the session is resolved
and logged once. The hook input doesn't tell two session starts of the same session, event and source
apart, for example two `/clear` in a row, or a retry after a start that didn't finish. So the parts are
pinned: a hook that starts within 3 seconds after they were written, or after a hook last read them, uses
them, and a hook that starts later resolves the session again, in a folder of its own. The hooks of one
session start begin within a second or so of each other, so they read one text, never parts of two. The
cost: a second session start within 3 seconds gets the text as it was at the first one, also when a file
changed in between; its log entry says `reused` with how much earlier. Each read starts the 3 seconds
again, so session starts that keep coming less than 3 seconds apart keep getting that first text, however
old it gets. The file stays after the hooks ended: a session start deletes the folders that no hook used
for a minute, never one that a hook used more recently. When the folder can't be written, for example
in a sandbox, each hook resolves the session on its own. Claude Code orders the texts by when the hooks
end, so each hook waits for the one before it, up to 2 seconds; the labels keep the text correct in any
order. Each hook's command says how many hooks its setup has (`--slot 3 --of 40`, and `ctxtra_slots` from
the Hermes plugin), and the text is split into at most that many parts.

A Claude Code or Muse hook without `--slot K --of N`, a Hermes plugin that doesn't send `ctxtra_slots`, and
a plugin that doesn't send `ctxtra_plugin` add nothing, and the log says why: no release writes such a
setup, and without the slot count each hook would get the whole text. `ctxtra hook` ignores command-line arguments it doesn't know, so hooks that a later
version set up still run after a downgrade.

When the binary that a hook runs is gone, Claude Code, Codex, Muse and Devin show `ctxtra: the ctxtra binary
that this hook runs is missing; reinstall ctxtra, or run ctxtra setup --agent claude with the new binary`
(with the agent's name) at session start, and Pi shows it as a warning. The other agents add nothing, without
a word. `ctxtra doctor` and the [setup notices](#setup-notices) show the missing binary. Setup leaves a config
alone that has a ctxtra hook changed by hand, and says so, instead of adding a second hook next to it.

## Experimental: MCP servers

> **Experimental, off by default.** ctxtra can give sessions MCP servers, scoped by folder like instructions and
> skills. It is work in progress: it is on only when `CTXTRA_EXPERIMENTAL_MCP=1` is set, and how the config
> declares servers and binds them is not decided. Without the variable, ctxtra ignores everything about MCP
> servers, and a config that declares them is an error like any unknown key.

`ctxtra setup` adds one MCP server named `ctxtra` to each agent. The agent starts it like any local MCP server.
It gives the session the tools of the MCP servers bound to the session folder, and forwards each call.

### The session's servers

- The proxy's working directory when the agent starts it is the session folder. The proxy resolves bindings for
  it with the same rules and the same config as the hook (`$CTXTRA_CONFIG` or the default file), so `dirs`,
  `repos` with `subpath`, and worktrees work the same. It never asks the agent for roots.
- The servers are fixed when the proxy starts, like the agents' own MCP servers. Changing directory, adding
  folders during the session (for example `/add-dir`), ctxtra-follow and config changes add or remove none.
- On resume, the agent starts the proxy in the folder of the resume command. Claude Code, Codex and Antigravity
  don't run the hook on resume, so after a resume from another folder, the instructions come from the old
  folder and the servers from the new one.
- The proxy offers only tools. It forwards tool lists (titles, annotations and schemas unchanged), calls,
  cancellation and progress, and a server's tool list changes. It doesn't forward a server's instructions,
  prompts or resources, and offers servers no roots.
- The proxy answers the agent's first tool list when every bound server has answered, or at the agent's
  deadline, with the tools of the servers that answered. The deadline counts from the proxy's start and is
  shorter than the agent waits for the list, because an agent that gives up first gets no tools of any server:
  1.5 seconds in Claude Code, 8 in Pi, 10 in the others (Codex waits that long because its entry is
  `required`). A server that answers later is added with a tool list change notification; agents that ignore
  it (Codex, Muse) don't get its tools in that session.
- A problem never breaks a session: with a broken config, a failing server or no bound server, the proxy starts
  and serves the tools it can, or none, and logs the problem. A server whose tool list has more than 2,000 tools
  or 8 MiB, or that pages without end, is left out. A server that ends during the session is stopped with the
  processes it started, and its tools go.

### Local servers

A local server runs as the proxy's child, in the session folder, in a process group of its own. Its
environment is only: its `env` values, its `env-pass` variables and secret values, and from the proxy's
environment `PATH`, `HOME`, `USER`, `LANG`, `TMPDIR`, `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`, `ALL_PROXY` (each
also lower case), `SSL_CERT_FILE` and `SSL_CERT_DIR`. Its stderr is dropped: it can hold secrets, and agents show
none of it.

The program must be an absolute path or a name in `PATH`: a relative path could come from the session's
repository. A tool like `npx` still prefers a project's own packages, so a repository can change what such a
server runs.

When the agent closes the proxy's input, or the proxy gets SIGTERM, running calls end, and each local server's
process group gets SIGTERM, and SIGKILL after 2 seconds if any process of the group is still there, also a
server that is still starting. When the proxy is killed, its servers get the end of their input, as when an agent is killed: a server
that exits then stops.

### Network and logs

The hook never uses the network. The proxy connects only to the bound remote servers and their authorization
servers. It logs to ctxtra's log (`$CTXTRA_LOG` or `~/.ctxtra/log.jsonl`) with the agent's name: its start,
the session folder, the bound servers, the number of config problems (`ctxtra doctor` lists them), each
server's connection, late servers, servers that end during the session and how each local server's process
ended, and its own end. It never logs tool arguments, results, secrets or config
problems as text; a failed call is logged by its kind or code only, because error messages can repeat the
arguments. `doctor`'s last hook runs leave these lines out.

A remote server is reached with streamable HTTP, and with the older SSE transport when that fails. ctxtra
checks every request, not only the configured URL:

- Requests go only to the server's origin. A redirect to another origin is refused, and so is an SSE message
  endpoint on another origin. Tokens and secret headers go only to the server; refresh tokens and client secrets
  only to the authorization server's token endpoint, which never gets a redirect followed.
- HTTPS only. Plain HTTP only to loopback, for a server on loopback.
- Addresses are checked when connecting, so a DNS change can't get around the check. Link-local addresses,
  such as the cloud metadata address `169.254.169.254`, are always refused. Private addresses (RFC 1918, CGNAT,
  ULA) are allowed only when the server itself is on one, and loopback only when the server is on loopback.
- `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` and `ALL_PROXY` (also lower case) are honored, and the certificates of
  `SSL_CERT_FILE` and `SSL_CERT_DIR` are trusted next to the system's. Through a proxy, ctxtra checks the target's
  addresses as this machine resolves them, and refuses a name that doesn't resolve here unless it is the
  server's own. The proxy resolves the name again: a proxy that resolves it to another address isn't covered.

### Sign-in

A remote server that answers a request without a token with 401 and a Bearer challenge uses OAuth; an older
SSE server is asked with GET. Only `ctxtra mcp add` and `ctxtra
mcp login`, in the user's terminal, sign in; the proxy and the hook never do. Without a terminal, or with
`--no-input`, they fail and change nothing; `--no-login` adds the server without signing in.

Sign-in follows the MCP authorization spec (2026-07-28) for clients:

- Discovery: the server's protected resource metadata, where its 401 says or at the well-known places, names
  the authorization server. Without that metadata, as with Atlassian, ctxtra looks for authorization server
  metadata at the server's origin. The metadata must name the issuer it was found for, offer PKCE with `S256`,
  and use HTTPS for every endpoint.
- Scopes: the server's configured scopes; else those of its 401; else those of its protected resource metadata.
  ctxtra adds `offline_access` when the authorization server lists it, so that it issues a refresh token, and
  the scopes of the last sign-in and of a 403 `insufficient_scope`.
- Client: the user's own app (`--client-id`, and `--client-secret` for its secret), else ctxtra's client ID
  metadata document, else dynamic registration. ctxtra's document isn't hosted yet, so ctxtra registers
  dynamically. A server that offers neither, such as GitHub or Slack, needs the user's own app.
- The browser opens the authorization link (`$BROWSER`, else `open` or `xdg-open`). ctxtra waits for the
  redirect on `127.0.0.1`, on a random port or on `--callback-port`, which is stored for later logins. It
  accepts one redirect with the right `state` and refuses others without ending the sign-in; it waits 5
  minutes. With `--no-browser`, ctxtra prints the link and accepts the address the browser ends at, pasted in
  the terminal, for example over SSH.
- The answer's `iss` must be the issuer, and is required when the authorization server says it sends it. The
  token request sends the `resource` parameter. A failed check stores nothing.
- A dynamic registration is reused while the redirect address is the same, and made again for a new one.

After sign-in, ctxtra shows the issuer, the server's notes, and the account when the provider's answer names
one. A second server of the same issuer gets a note before the browser opens: the provider may use the
browser's active account, so a private window helps to sign in with another one. When no refresh token came,
ctxtra says the user signs in again when the sign-in expires.

In a session, a server that needs sign-in at its start has no tools; the proxy reads its state file every 3
seconds and adds its tools once it is signed in (Codex ignores the list change: a new session gets
them). A refresh token that the provider rejects, or a 401 after one refresh, fails the call with `run ctxtra
mcp login NAME`. A 403 `insufficient_scope` records the scopes it asks for, for the next login, and fails the call
the same way.

`ctxtra mcp login NAME` signs in again; for a server without OAuth, or with `--secrets`, it asks for the secret
values again. `ctxtra mcp logout NAME` deletes the tokens and secret values on this machine, like the agents'
own logout; the provider isn't told, so the grant stays valid there until it expires or the user removes it.

### Sign-in state and missing tools

The session summary names each bound server whose state file says it needs sign-in, after an empty line, like
the [notices](#notices):

```text
⚠ linear_work needs sign-in: run ctxtra mcp login linear_work
```

The hook reads only the state files: no key, no keychain, no network. A server that no session has tried yet
has no state, and no line.

The proxy records, per session folder, what the last session started there got from each bound server: its
tools in the first tool list, later (too slow for it, or signed in during the session; Codex doesn't
get those tools), or none, and why. `ctxtra mcp status [DIR]` shows that record with the folder's servers and
their sign-in state, from local files only:

```text
the last session here (codex, 2026-11-10 14:08:19) waited 10.0 s for the first tool list:
  linear_work  ready after 0.4 s
  pg  later: too slow for the first tool list, after 15.2 s; Codex sessions didn't get its tools
```

The records are in `~/.ctxtra/mcp/status`, one per folder, so a session in one folder never changes another's.

The agent learns it too: the proxy's instructions (the MCP `instructions` field, which it sends after waiting
at most half a second for the folder's servers) name the bound servers and say to run `ctxtra mcp status` when
a tool of theirs is missing, and name each server whose state says it needs sign-in. Claude Code shows them in
the system prompt; Codex in the description of the `ctxtra` server's tools, so only when the session has at least
one of them.
`ctxtra mcp list` and `doctor` show each server's sign-in state and where the key of the credentials is, with the
warning for a key file.

### Credentials

A server's secret values, its OAuth client registration and its tokens are stored in `~/.ctxtra/mcp`, never in
the config or in an agent's config. ctxtra asks for secret values in the terminal without showing them, or
reads them from stdin, one per line.

| Path | Content |
|---|---|
| `credentials/<server>.v1.age` | The server's credentials, encrypted. `v1` is the format version. |
| `state/<server>.json` | No secrets: whether the server is signed in, and why it needs sign-in. |
| `locks/<server>.lock`, `locks/.store.lock` | Locks for changes of one server's files, and for creating the key. |
| `key.json` | No secrets: where the key is, `keychain` or `file`. |
| `key` | The key, only when `key.json` says `file`. |

The folder is readable by the user only (0700), and the files too (0600). Every save replaces the file in one
step and is synced to the disk before ctxtra uses the new values: a failed save fails the command or the
call.

The key is an [age](https://age-encryption.org) X25519 key in the OS keychain: the login keychain on macOS, the
Secret Service on Linux (service `ctxtra`, account `mcp-credentials`). Without a usable keychain, the key is the
file `key`, and ctxtra warns that any process of the user can read the credentials then. Only the commands that
set secret values or sign in create the key, and they record its place in `key.json`. Every other command and
the proxy use that place and never create or move the key. When the keychain that holds the key can't be
reached, for example over SSH or without a session bus, ctxtra says so and doesn't fall back to a file. A
file key stays the key when a keychain becomes usable later. The proxy waits for the keychain at most 3
seconds, and on Linux never unlocks a locked Secret Service collection, which would show a prompt. macOS can't
read a locked keychain without a prompt; the login keychain is unlocked while the user is logged in. When the
proxy can't get the key, the servers that need it are left out. When the
key is gone, the credentials can't be read: the next command that creates the key moves them to
`credentials/unreadable`, and their servers need their values or sign-in again.

Each server's credentials record its name and its URL, or its command for a local server. When the server's
definition changes, ctxtra sends nothing and the values must be set again. Two configs (`$CTXTRA_CONFIG`) with
a server of the same name and URL share its credentials; use different names for different accounts.
Removing a server deletes its credentials.

Sessions in every agent refresh a server's tokens at the same time safely: a refresh holds that server's lock
while it reads the credentials again, asks for new tokens and saves them, and other sessions wait for it and
use the new tokens. Every other change of a server's credentials takes the same lock. The locks use `flock`,
which can be unreliable on a network home folder (NFS).

The hook never reads the credentials or the key. Like the agents' own MCP sign-ins, the credentials protect
against backups, sync tools and casual reads, not against the user's own processes: any process of the user
can read the key, through the keychain's command-line tool or the key file.

The file names, the format version in them, the lock paths and the state file's meaning are a contract between
ctxtra versions, so sessions of an older and a newer ctxtra can refresh the same server. Within a format
version, ctxtra only adds fields and keeps the fields it doesn't know. An older ctxtra that finds credentials in
a newer format fails that server's calls with an upgrade hint, and changes nothing.

### Setup

With `CTXTRA_EXPERIMENTAL_MCP=1` and at least one MCP server in the config, `ctxtra setup` adds the entry to each
agent it sets up. It never copies the user's servers into agent configs. Without servers, it adds no entry and
keeps one that is there; `--remove` removes it.

| Agent | File | Entry |
|---|---|---|
| Claude Code | `~/.claude.json` (`$CLAUDE_CONFIG_DIR/.claude.json`) | `mcpServers.ctxtra: {type: stdio, command, args}` |
| Codex | `~/.codex/config.toml` (`$CODEX_HOME`) | `[mcp_servers.ctxtra]`: `command`, `args`, `env_vars`, `startup_timeout_sec`, `required`, between marker comments |
| Cursor | `~/.cursor/mcp.json` | `mcpServers.ctxtra: {command, args}` |
| OpenCode | `~/.config/opencode/opencode.json` | `mcp.servers.ctxtra: {type: local, command, pool: false}` |
| Pi | `~/.pi/agent/mcp.json` | `mcpServers.ctxtra: {command, args}` |
| Antigravity | `~/.gemini/config/mcp_config.json` | `mcpServers.ctxtra: {command, args}` |

Muse isn't supported yet: it starts MCP servers with a cleared environment, so ctxtra's server would get
neither `CTXTRA_EXPERIMENTAL_MCP` nor ctxtra's other settings. Setup adds no entry to it.

Agents write these files themselves too (Claude Code often writes `~/.claude.json`): when a file changes
between setup's read and its write, setup makes its edit again on the new content, so the agent's change stays.

The command is the ctxtra binary and its arguments are `mcp proxy AGENT`. Setup owns only the keys in the table
and keeps every other key the user adds to the entry, such as an approval setting. An entry named `ctxtra` that
doesn't run `ctxtra mcp proxy` is the user's: setup and `--remove` leave its file alone and say so.

In Codex, the entry is a block between `# >>> ctxtra MCP server` and `# <<< ctxtra MCP server` lines. Setup
rewrites only its own keys' lines in the block, and keeps the rest of the file byte for byte. `--remove` removes
the block, keys you added to ctxtra's table after it, and `[mcp_servers.ctxtra.*]` tables. Before it writes,
it checks that the new file is valid TOML and says the same as the old one but for those keys; otherwise it
changes nothing, because Codex refuses an invalid config. Codex starts MCP servers with an allow-listed
environment, so `env_vars` lists ctxtra's variables (`CTXTRA_CONFIG`, `CTXTRA_LOG`, `CTXTRA_EXPERIMENTAL_MCP`),
the Secret Service's (`DBUS_SESSION_BUS_ADDRESS`, `XDG_RUNTIME_DIR`), the proxy and certificate variables, and
every local server's `env-pass` names. `startup_timeout_sec` is 30, and `required = true`: Codex waits about 1
second for an optional server's tools, and for a required one up to `startup_timeout_sec`. A required server that
can't start stops the Codex session, so a moved or deleted ctxtra binary does too, until `ctxtra setup` runs again;
the setup notices report it.

OpenCode's entry isn't pooled, so its shared service starts one proxy per session folder.

The entries, the keys setup owns, and the arguments of `ctxtra mcp proxy` are part of the setup contract (see
[Setup compatibility](#setup-compatibility)): every later version accepts them, and the proxy ignores arguments
it doesn't know.

The setup notices and `doctor` report the entry:

| When | Notice |
|---|---|
| Servers were added after setup | `claude: the ctxtra MCP server is missing; run ctxtra setup --agent claude` |
| The entry runs another binary | `claude: the ctxtra MCP server runs /usr/local/bin/ctxtra, not this ctxtra (/opt/homebrew/bin/ctxtra); to switch, run ctxtra setup --agent claude` |
| The entry is gone from its file | `claude: the ctxtra MCP server is gone from ~/.claude.json; if that wasn't on purpose, run ctxtra setup --agent claude; ...` |
| Codex's entry doesn't pass a local server's `env-pass` variable | `codex: the ctxtra MCP server doesn't pass DBHOST to local servers; run ctxtra setup --agent codex` |
| An entry named ctxtra isn't setup's | `~/.claude.json: the ctxtra MCP server doesn't run ctxtra mcp proxy; rename or delete it, then run ctxtra setup --agent claude` |

### Tool names

A session sees each tool as `<server>__<tool>`. The name uses only `A-Z`, `a-z`, `0-9` and `_`, and is at most
38 characters: Muse shortens longer tool names and then can't call them, and the strictest model APIs allow 64
characters, of which Claude Code and Pi use 13 for `mcp__ctxtra__`.

- A server name is 1 to 20 characters of `a-z`, `0-9` and `_`, starts with a letter, and has no `__` and no `_`
  at the end.
- A tool name that has other characters, or makes the whole name too long, is shortened: other characters become
  `_`, the name is cut to fit, and `_` and 6 hex digits of a SHA-256 hash of the server and the tool name are
  added. The cut never goes into `<server>__`.
- The name depends only on the server and the tool, so it is the same in every session, agent and machine, and
  the agents' permission rules keep working. Only when two names in one session would be equal, the shortened
  ones get longer hashes: 10 digits, then up to 16.
- A shortened tool's description starts with its server and its own name: `[gh: create-issue] Create an issue.`

| Server | Tool | Session sees |
|---|---|---|
| `linear_work` | `list_issues` | `linear_work__list_issues` |
| `gh` | `create-issue` | `gh__create_issue_ef677c` |

## Setup compatibility

What `ctxtra setup` writes is a contract between the agents and the `ctxtra` binary: the hook entries
(events, matchers, command lines, the slot count, timeouts, other keys such as `additionalContextLimit`), the
plugin files (the events they send, their input fields, the answer fields they read, their constants), and the
arguments of `ctxtra hook`. A new version changes only the binary, and the binary keeps serving every setup
that a released version wrote with what that setup was promised.

The contract holds from 0.1, the first release, on.

An upgrade keeps existing setups working with the same capacity. New features may need another
`ctxtra setup`; a capacity increase may need it too. In either case, the release notes explain when to
run setup, and `ctxtra doctor` reports what the existing setup is missing. In Codex, changing the hooks
also requires trusting them again.

The experimental MCP entry (see [Experimental: MCP servers](#experimental-mcp-servers)) and the arguments of
`ctxtra mcp proxy` are part of the contract too.

Answers only grow: fields are added, never renamed or given a new meaning, and a field that a released setup
reads is sent while that setup is supported. The hooks and plugins ignore fields they don't know. The
arguments and event names that a released setup sends are accepted forever, and `ctxtra hook` ignores
arguments it doesn't know, so an older binary still runs a newer setup.

### Guided onboarding

`ctxtra onboard` is the interactive getting-started flow. It previews the hook file changes
and asks before installing hooks (with the same backups and agent detection as `ctxtra setup`),
explains and offers each bundled skill that isn't already bound, then offers instruction
import if no binding adds instructions yet.
When the ctxtra skill is bound and an installed agent can be started, it asks whether to
start one and lets the user choose among available agents. Import uses the same backups,
prompt and launcher as `ctxtra import --start AGENT`. Declining a step leaves it unchanged.
Declining hook installation stops onboarding before any changes. Horizontal rules separate
the remaining steps from the preceding output. EOF never accepts a question. Rerunning
onboarding skips skills that are already bound.

Onboarding requires terminal input and output and rejects `--no-input` before making changes.
Scripts and agents use `setup`, `skill install` and `import` separately. `setup` remains
non-interactive and prints pending commands plus a pointer to guided onboarding.

### Setup notices

After every command that runs, but `hook`, `setup`, `onboard`, `doctor`, `source check`, `check-updates`, `help`, `version` and `completion`, ctxtra
prints a line on stderr for each setup that needs you, with the command that repairs it. `--help` and usage
errors print none. The lines never change the exit code; `doctor`
shows the same lines under each agent instead:

| When | Notice |
|---|---|
| The binary in the setup is gone | `claude: the hooks run ~/Downloads/ctxtra-0.3/ctxtra, which doesn't exist; run ctxtra setup --agent claude` |
| The setup runs another binary | `claude: the hooks run /usr/local/bin/ctxtra, not this ctxtra (/opt/homebrew/bin/ctxtra); to switch, run ctxtra setup --agent claude` |
| An older version set it up, with less room (after a capacity change) | `codex: set up by an older ctxtra, with Codex's limit of 1,000,000 bytes; run ctxtra setup --agent codex (Codex will then ask you to trust the hooks again)` |
| Part of the setup is missing | `claude: SessionStart has 3 of the 40 ctxtra hooks; run ctxtra setup --agent claude`, `dsh: the patch row that loads ctxtra's plugin is missing; run ctxtra setup --agent dsh` |
| The Hermes plugin isn't enabled | `hermes: the ctxtra plugin is installed but not enabled; run hermes plugins enable ctxtra` |
| A setup is gone from its file | `claude: ctxtra's hooks are gone from ~/.claude/settings.json; if that wasn't on purpose, run ctxtra setup --agent claude; if it was, ctxtra setup --remove --agent claude stops this notice` |
| An agent was found since the last setup | `cursor: found since the last ctxtra setup; to add ctxtra to it, run ctxtra setup --agent cursor` |

A new agent is told about once: ctxtra then adds it to `detected` in `~/.ctxtra/setup.json`, unless a setup
is running or the file can't be written, and then tells again next time. `doctor` still lists it as not set
up. Each command names its agent, so a repair doesn't set up agents you left out, and a command that changes
Codex's hooks says that Codex will ask to trust them. The binary isn't compared when this ctxtra runs from a
temporary folder (`go run`). Apart from that record, the notices only read files: regular files of at most
16 MiB, so a FIFO or a huge file in place of a config is one line that it can't be checked, never a command
that hangs.

`setup` records in `~/.ctxtra/setup.json` where it set up each agent (`installed`, by config path, until
`setup --remove` removes it) and every agent it found (`detected`, also those left out with `--agent`). The
notices use it to tell a setup that is gone from one that never was, and to find new agents (and add the ones
they told about to `detected`): without the file there are no such notices, and a broken one is one notice, `~/.ctxtra/setup.json is unreadable: notices
about new or removed setups are off until the next ctxtra setup`. The file never decides a change of an
agent config. One setup runs at a time (`~/.ctxtra/setup.lock`); `--dry-run` doesn't write the file. When
setup can't write it, setup fails with exit code 1, and the config changes it made stay.

## Validation

Problems never make ctxtra add more than the config says:

- A config with a wrong `schema`, an invalid `budget`, `check-updates`, source or `include`, or broken YAML,
  is ignored as a whole.
- An invalid key or value in `summary-message` is a problem, and the default applies to that key; the other
  keys still apply. It changes only what the summary shows. A key with no value is invalid; a missing key
  gets the default.
- A broken binding is skipped, in the config or in a binding file. The other bindings in the same file
  still apply.
- A binding file that can't be read as a whole is skipped as a whole: a wrong `schema`, a key other than
  `schema` and `bindings`, a YAML syntax error, more than one YAML document, or YAML anchors.
- Unknown keys, duplicate keys, null values, empty lists, YAML anchors and aliases are errors.
- A missing file is reported. The session still starts.
- A file that isn't trusted (see [Sources and trust](#sources-and-trust)) is skipped and reported.

`ctxtra doctor` lists every problem, also in bindings that don't match its folder, and exits with 1. The
`context` commands report only what a session in their folder gets.

## Command line

`ctxtra help` lists the commands, and `ctxtra help COMMAND` (the same as `ctxtra COMMAND --help`) shows one
with its flags and examples. The commands that change the config are grouped by what they change:
`source`, `binding-file` and `binding`, each with `add`, `list` and `remove`.

- Exit codes: 0 when the command did what it was asked; 1 when it failed, or when `doctor` or a `context`
  command found that a session wouldn't get everything it should (for `doctor`, a session in any folder),
  `source check` found an error, or a check of `check-updates` failed; 2 for a usage error (an unknown command
  or flag, a missing argument), with the command's usage hint on stderr.
- Results go to stdout; problems, warnings, notices and questions go to stderr. In a report (`doctor`,
  `context explain`, the review of `source update`, `source check`), its problems and warnings are part of the result, on
  stdout.
- `--json` (on `doctor`, `context files`, `context explain`, the `list` commands, `source show`,
  `source update --check` and `source check`) prints one JSON object: `schema_version`, `data`, and `diagnostics`, a list of
  `{severity, message}` with the severity `error`, `warning` or `notice`. New fields can be added; a field
  doesn't change its meaning within a `schema_version`.
- ctxtra asks a question only when stdin and stdout are a terminal. `--no-input` turns every question off:
  a step that needs an answer then fails. No option answers a question for you.
- `--dry-run` on the commands that change the config, the agents' setup or the installed skills checks the
  change and shows it, and writes nothing.
- `--config FILE` uses another config for one command, like `$CTXTRA_CONFIG`. `setup` and `onboard` don't take
  it: the hooks carry no config path, so sessions read `$CTXTRA_CONFIG` or the default file.
- Paths in arguments are relative to the current folder; in the config they are relative to the config's
  folder.

## Limits

| Limit | Value |
|---|---|
| Any one file (config, binding file, instruction file, `SKILL.md`) | 4 MiB |
| Everything read for one session, the config and binding files included | 16 MiB |
| Text an agent gets in full | its capacity, see [Capacity](#capacity) |
| Skill description | 1024 bytes, longer ones are cut and end with `…` |

## Reference

**Config**

| Key | Required | Meaning |
|---|---|---|
| `schema` | yes | `ctxtra/v1` |
| `sources.<name>.trusted-path` | no | A folder of your own files. Absolute or starts with `~`. |
| `sources.<name>.git` | no | A git repository: `https://`, `ssh://` or `user@host:path`. |
| `sources.<name>.branch` | with `git` | The branch `source update` follows. |
| `sources.<name>.local-git` | no | Your clone, pinned to its committed `HEAD`. Absolute or starts with `~`. |
| `sources.<name>.commit` | no | With `git` or `local-git`: the accepted commit, a full ID in quotes. |
| `sources.<name>.check-updates` | no | With `git` or `local-git`: `true` to have sessions tell you about new commits. Default `false`. |
| `bindings[]` | no | Your own bindings, see **Binding** below. Paths are relative to the config's folder. |
| `include[]` | no | Binding files. Relative to the config's folder; `~` and `ctxtra://` work. |
| `budget.max-chars` | no | Integer ≥ 0, in characters. 0 or missing: no limit. |
| `check-updates` | no | `false` to stop checking for new ctxtra releases. Default `true`. |
| `summary-message.show-instructions` | no | `false` leaves the Instructions list out of the session summary. Default `true`. |
| `summary-message.show-skills` | no | `false` leaves the Skills list out of the session summary. Default `true`. |
| `summary-message.max-instructions` | no | Integer ≥ 0: how many files of each source the summary lists, then `+N more`. 0: each source with its number of files only. Default 30. |
| `summary-message.max-skills` | no | The same for skills. Default 30. |

**Binding file**

| Key | Required | Meaning |
|---|---|---|
| `schema` | yes | `ctxtra/v1` |
| `bindings[]` | yes | See **Binding** below. Paths are relative to the binding file's folder. |

**Binding**

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | Letters, digits, `.`, `_`, `-`. Unique in the file. |
| `match.global` | `match` needs one key | `true`: every session. |
| `match.dirs[]` | `match` needs one key | Folder patterns. |
| `match.repos[]` | `match` needs one key | A remote pattern, or `remote` with an optional `subpath`. See [Match](#match). |
| `instructions[]` | `instructions` or `skills` | Instruction files. |
| `skills[]` | `instructions` or `skills` | Skill folders. |

**Environment**

| Variable | Default |
|---|---|
| `CTXTRA_CONFIG` | `~/.config/ctxtra/config.yaml` |
| `CTXTRA_LOG` | `~/.ctxtra/log.jsonl` |

Muse Code runs hooks with a cleared environment, so its sessions always use the defaults.

The session folder is the `cwd` the agent sends to the hook. If it is empty: the first `workspace_roots`
entry (Cursor), else the first `workspacePaths` entry (Antigravity, which runs hooks in the hook file's
folder), else the hook's working folder. DSH's home folder is `$DSH_HOME`, or `~/.dsh`.
