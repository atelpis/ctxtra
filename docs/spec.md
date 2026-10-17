# Config spec: ctxtra/v1alpha1

> **Preview.** `v1alpha1` may change without a migration path. ctxtra does not protect you from the
> content of the files you bind: an instruction file or skill is acted on like anything you type to the
> agent. Bind only files you wrote or reviewed.

For how this is meant to be used, step by step, see [scenarios.md](scenarios.md).

## Terms

- **Session folder:** the folder an agent session starts in.
- **Binding:** a document of kind `Binding`. It says: in sessions that match this selector, add these
  instruction files and these skills.
- **Binding file:** a YAML file with one or more bindings, separated by `---`.
- **Config:** the file that lists which binding files you use, and declares your sources.
- **Source:** a folder of your own files (trusted), or a team git repository pinned to a commit you
  accepted (pinned), declared in the config. Files in it are referenced as `ctxtra://<source>/<path>`.

Instruction files are plain Markdown. Skills are folders with a `SKILL.md`, the same format other agent
tools use. Neither has anything specific to ctxtra.

## Config

`~/.config/ctxtra/config.yaml`, or the file in `$CTXTRA_CONFIG`.

```yaml
apiVersion: ctxtra/v1alpha1
kind: Config

sources:
  dotfiles:
    trusted-path: ~/dev/dotfiles      # a repo of your own: read from disk, changes apply right away
  team-ctx:
    git: git@github.com:acme/team-ctx.git
    branch: main                      # where sync looks for new commits
    commit: '9f8e7d6c5b4a39281706f5e4d3c2b1a098765432'   # the accepted version; sync changes it

bindings:                             # binding files, applied in this order
  - bindings/personal.yaml            # relative to this file's folder
  - ctxtra://dotfiles/ctxtra/go.yaml  # a file in the dotfiles source
  - ctxtra://team-ctx/bindings/billing.yaml

budget:
  maxChars: 40000                     # optional; 0 or missing = no limit
```

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
`pinned-path`.

`ctxtra://<source>/<path>` works in `bindings` and in `files` and `skills` of any binding file. The path as
written must stay inside the source: `..` that leaves it, and absolute paths, are errors. For a trusted
source it is the same as the path under the source's folder. A symlink there is followed by the rules
above, so it can lead outside the source, but only to a file that is trusted on its own.

## Pinned sources

A `git` source is a team repository. ctxtra keeps its own copy, so you don't need a clone, and serves one
commit of it: the one in `commit`. A new commit on the branch reaches your sessions only after you review
and accept it with `ctxtra sync`, or after you change `commit` yourself.

```text
$ ctxtra add git@github.com:acme/team-ctx.git bindings/billing.yaml
fetched team-ctx: main at abc1234 (git@github.com:acme/team-ctx.git)
added source team-ctx (pinned: changes apply after `ctxtra sync`)
added ctxtra://team-ctx/bindings/billing.yaml
  binding billing: for github.com/acme/billing
    files: services-overview.md
```

- `git` is an `https://` or `ssh://` URL, or `user@host:path`. Other transports and local paths are errors.
- `branch` is required. `add` uses the remote's default branch unless you pass `--branch`.
- `commit` is a full commit ID in quotes: 40 hex digits, or 64 for SHA-256 repositories. A source without
  `commit` is inactive until `ctxtra sync <name>`, which accepts the branch as it is, like `add`.
- **The first version is accepted without review**, the same decision as cloning the repository: read a
  team repository before you add it. `add` shows its bindings and its executable files.
- Accepting a commit accepts **every file in it**, also files the review lists only by name: a file you
  reference later is used at once, without another review.
- Anyone who can edit your config can choose the version, for example through a dotfiles repository that
  others push to. A `commit` copied from another laptop was reviewed there.
- A binding file in a pinned source may only reference files in the same source: relative paths, or
  `ctxtra://` with its own name. Absolute paths, `~`, `..` that leaves the source and other sources are
  errors, and the binding is skipped. A team repository can't add your files to a session, and can't
  declare sources: only your config does.
- Files are read from ctxtra's store (below), never from a clone. Symlinks and submodules in the
  repository are left out.
- ctxtra doesn't check for new commits by itself: sessions never fetch. Run `ctxtra sync` regularly.

### Commands

| Command | Does |
|---|---|
| `ctxtra add <git URL> [<binding file>] [--as NAME] [--branch B]` | Fetches the branch, adds the source with its current commit, and the binding file to `bindings`. The name defaults to the repository name. Without a binding file, it lists the binding files in the repository. |
| `ctxtra add ctxtra://<source>/<binding file>` | Adds a binding file of a source you have, at its accepted commit. |
| `ctxtra add <binding file> [--trust] [--as NAME]` | Adds a binding file on your disk. A file in a clone with a remote is imported as a `git` source (below); with `--trust`, the repository is added as a `trusted-path` source instead. |
| `ctxtra add --trust <folder> [--as NAME]` | Adds a folder as a `trusted-path` source. |
| `ctxtra add --pinned-path <clone> [--as NAME]` | Adds your clone as a `pinned-path` source (below). |
| `ctxtra remove <source>` | Removes the source and its `bindings` entries, and lists the lines of your binding files that still use it. |
| `ctxtra remove <bindings entry>` | Removes one entry from `bindings`. |
| `ctxtra sync [--full] [SOURCE]...` | For each pinned source: makes sure the accepted commit is in the store, fetches the branch, shows what changed (below), and on `y` writes the new commit to the config. |
| `ctxtra sync --restore [SOURCE]...` | Fetches exactly the commits in the config, and changes nothing in it. Use it on a new laptop, or when the store is gone. It fails when a commit can't be fetched (removed by a force-push, or never pushed). |

### A clone you already have

```text
$ ctxtra add ~/dev/team-ctx/bindings/billing.yaml
~/dev/team-ctx is a clone of git@github.com:acme/team-ctx.git (not trusted), added as source team-ctx
starts at your local commit 9f8e7d6; later sync follows origin/main (remote not checked yet)
added ctxtra://team-ctx/bindings/billing.yaml (pinned: changes apply after `ctxtra sync`)
```

- A binding file in a clone with a remote is imported: a `git` source with the clone's remote and the
  upstream of its current branch, starting at the clone's committed `HEAD`. Uncommitted changes are not
  used. Later, `sync` follows the remote branch, not the clone. A clone with several remotes, or without an
  upstream, needs `--remote` or `--branch`.
- Paths under the clone in your own bindings keep working, and are served from the source:
  `~/dev/team-ctx/jira-ticket.md` is `ctxtra://team-ctx/jira-ticket.md`, also after the clone moves or is
  deleted. `doctor` suggests the `ctxtra://` form. This mapping is kept in `~/.ctxtra/mappings.json`, by
  the source's name and URL; it is never guessed from a matching remote. A `trusted-path` source that covers
  the folder comes first.
- A missing file under the clone is skipped, not read from disk. When the source is removed, renamed or its
  URL changes, paths under the clone are skipped with `not in the config any more`, not read from disk.
  `ctxtra remove` lists them.
- If `mappings.json` is deleted, paths under old clones are ordinary paths again: checked as usual, and
  skipped in a repository with a remote. If it is broken, ctxtra can't tell which paths were mapped: every
  plain path outside your `trusted-path` sources is skipped, and ctxtra doesn't write the file, until you
  fix or delete it.
- Reading the clone never fetches: git runs without transports and without lazy fetching.
- If the repository is yours (your dotfiles), `ctxtra add --trust <the file>` converts the source to
  `trusted-path` under the same name: its `ctxtra://` references are read from disk from then on. With
  `--as NAME`, a separate trusted source is added instead, and the pinned one stays: paths under the clone
  are then live, `ctxtra://` references to the pinned source stay pinned. Use this to edit the team
  repository and see your uncommitted changes.

### Your clone, pinned

```yaml
sources:
  team-ctx-dev:
    pinned-path: ~/dev/team-ctx
    commit: '1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d'
```

A `pinned-path` source is pinned like a `git` source, but `sync` follows the clone's committed `HEAD`
(detached works too) instead of a remote: for trying your commits before you push them. ctxtra fetches the
commit from the clone; it never copies the clone's working tree, index, config or hooks. It adds no mapping:
reference its files as `ctxtra://team-ctx-dev/<path>`.

### Review

```text
$ ctxtra sync
team-ctx: abc1234 -> def5678 (main, git@github.com:acme/team-ctx.git)
WARNING: binding billing in bindings/billing.yaml now applies to every session (was: github.com/acme/billing)
diff --git a/bindings/billing.yaml b/bindings/billing.yaml
...
also changed, not used by you:
  changed  README.md
  added    tools/setup.sh  executable
Accepting accepts these files too: a file you reference later is used without review. ctxtra sync --full shows them.
Accept? [y/N]
```

- Shown in full: the files your config uses from the source with the accepted commit or with the new one,
  under any selector. That is the binding files in `bindings`, every file and skill they reference, the
  source's files and skills in your own bindings, and every file of a used skill. A reference that the new
  commit removes shows too.
- Every other changed file is listed by name with the kind of change: added, removed, changed, or mode.
  `--full` shows them in full too.
- A warning for every binding in the source's binding files whose selector changes, and for every new one:
  the selector decides which sessions get the files.
- Errors in the source's binding files that the new commit brings, for example a path outside the source.
- The new commit is written to the store before the review. A commit over the limits fails there.

When you answer `N`, ctxtra remembers the commit as dismissed. Sessions that use the source, `files` and
`doctor` say `def5678 not accepted (dismissed)`. The next `sync` shows the review again.

### Notices

Sessions never fetch, so they can't know about new commits. When a session uses a pinned source that
wasn't checked for 7 days, it says `team-ctx last checked 12 days ago; run ctxtra sync to check for
changes` (Claude Code, Codex, Muse and Pi show it at session start, and `files` and `doctor` show it too). A
check is a fetch of the branch that worked, by `add` or `sync`. A source that was never checked on this
machine, for example after `sync --restore` on a new laptop, says so.

When a new commit doesn't continue the accepted one, `sync` warns: the branch was force-pushed. Accepting
it is still your choice.

`add` and `sync` change the config as text: they replace the `commit` value or add lines, and keep your
comments and layout. In a flow-style map (`{...}`) they write the file again from its YAML, which keeps
comments but not the layout. A changed config is checked before it is written, written atomically, and the
old one is kept in `~/.ctxtra/backups`. A symlinked config stays a symlink. If the source changed in the
config while `sync` waited for your answer, nothing is accepted.

### Store

- `~/.ctxtra/git/<id>.git`: a bare repository per source.
- `~/.ctxtra/store/<id>/<commit>/`: the files of each accepted commit, and of each commit `sync` showed you,
  private and read-only. Sessions
  read only these, and only through `ctxtra://`: a plain path into the store is an error, because a
  pinned source's binding file read that way could reference any file. A missing commit is reported
  (`run: ctxtra sync --restore`), and the references to it are skipped; the rest of the binding still
  applies.
- `<id>` comes from the URL: a source whose URL changes doesn't use the old data.
- A commit is deleted from the store 30 days after the last session that used it started (`sync` cleans
  up). The accepted commits are never deleted: of this config, and of every config that `add` or `sync`
  used (with `$CTXTRA_CONFIG`, for example), listed in `~/.ctxtra/configs.json`. If one of those, or that
  list, can't be read, nothing is deleted. A session marks a commit as used before it reads from it; while
  cleaning decides about that commit, the session waits up to a second, and else skips the source's files
  for this session. A session that runs longer than 30 days on an old commit can lose its skill files.
- `~/.ctxtra/state.json`: per source, the last check and the dismissed commit. Losing it loses the notices.
- `~/.ctxtra/mappings.json`: the folders of imported clones (above). `~/.ctxtra/configs.json`: the config
  files `add` and `sync` used.

### How ctxtra runs git

- It runs your `git`, so ssh keys, the ssh agent and credential helpers work. Only `add` and `sync` run
  git, and they may prompt. Sessions never fetch.
- Only the https and ssh transports, and the file transport only to fetch from a clone you named (an
  imported clone, a `pinned-path` source). No hooks, no submodules, no automatic maintenance. Nothing is checked
  out, so `.gitattributes` filters (git-lfs, smudge) never run. Diffs use no external diff and no textconv.
- Environment variables that point git at another repository or config are cleared.
- Every file path in a commit is checked before anything is written: no `..`, no `.git`, no control
  characters, no two file or folder names that a case-insensitive or Unicode-normalizing disk would merge.
  A commit over the limits (10,000 entries, 10 MiB per file, 100 MiB in total) is refused as a whole. A
  repository larger than 1 GiB with its history is refused too, and git's output is limited.
- Control characters in diffs, paths and messages are shown as escapes, so they can't hide lines in your
  terminal.

## Binding file

```yaml
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata:
  name: soul                          # unique in this file
spec:
  selector:
    global: true                      # every session
  files:
    - ~/notes/agents/me.md
---
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata:
  name: work
spec:
  selector:                           # matches if ANY entry matches
    dirs:
      - ~/dev/work/**
    gitRepos:
      - remote: github.com/acme/mono
        subpath: services/billing/**  # remote AND subpath
      - remote: github.com/acme/**
  files:
    - ../notes/work.md                # relative to this binding file's folder
  skills:
    - ~/skills/deploy                 # a folder with a SKILL.md
```

## Selector

A selector needs at least one key. The binding applies if **any** entry of **any** key matches.

| Key | Matches when |
|---|---|
| `global: true` | Always. `false` is an error: remove the key instead. |
| `dirs` | A pattern matches the session folder, or, inside git, the repository's main checkout. The second case makes worktrees match wherever they are on disk. Each pattern is absolute or starts with `~`. Symlinks in the fixed part of a pattern are resolved. |
| `gitRepos` | The session is in a git repository with a remote that matches `remote`, and, if `subpath` is set, the session folder relative to the worktree top matches `subpath` (`.` at the top). |

Patterns use [doublestar](https://github.com/bmatcuk/doublestar) globs: `*` stays inside one path segment,
`**` matches any number of segments, `x/**` also matches `x`, and `{a,b}` works. A pattern without `/**`
matches only that exact folder.

For a submodule, the main checkout is the submodule's folder. For a repository made with
`git init --separate-git-dir`, git doesn't record where the main checkout is, so `dirs` matches only the
session folder there.

If git doesn't answer within 3 seconds, the session counts as outside git: `gitRepos` don't match, and every
other binding still applies.

Remotes are compared in a short, lower-case form: `git@github.com:Acme/Svc.git` and
`https://github.com/acme/svc` are both `github.com/acme/svc`. A repository with several remotes matches if
any of them matches.

## Files and skills

- `files` and `skills` are lists. A binding needs at least one of them.
- Each path is absolute, starts with `~`, is relative to the binding file's folder, or is a
  `ctxtra://<source>/<path>` reference.
- Globs are not supported: list each file and skill folder by name.
- Each file is added once per session, at its first position, and the same for each skill. Two skills with
  the same `name`: the first is used, the other is reported.
- `SKILL.md` must start with YAML frontmatter that has a `description`. `name` is optional (default: the
  folder name). Other keys are ignored.
- Claude Code, Codex, Muse, Cursor and Copilot get the file contents and a list of the skills (name, description, path),
  and read a `SKILL.md` when a task needs it. Pi gets files as context files and skills as native skills.
  OpenCode gets files in the system prompt and skills as native skills, which its `skill` tool loads.

## Order and budget

Bindings apply in config order: the `bindings` list, then the documents of each file in order.

With `budget.maxChars`, whole files are dropped from the end until the added text fits. The budget counts
characters (Unicode code points), not bytes, and covers the whole added text, headers included. The skill
list is never dropped. `ctxtra explain` shows what was dropped.

## Validation

Problems never make ctxtra add more than the config says:

- A config with a wrong `apiVersion` or `kind`, an invalid `budget` or source, or broken YAML, is ignored as
  a whole.
- A broken binding is skipped. The other bindings in the same file still apply. This includes a document
  that isn't a map or uses anchors. A YAML syntax error skips the whole file, because the parser can't tell
  where the next document starts.
- Unknown keys, duplicate keys, null values, empty lists, YAML anchors and aliases are errors.
- A missing file is reported. The session still starts.
- A file that isn't trusted (see [Sources and trust](#sources-and-trust)) is skipped and reported.

`ctxtra doctor` lists every problem.

## Limits

| Limit | Value |
|---|---|
| Any one file (config, binding file, instruction file, `SKILL.md`) | 1 MiB |
| Everything read for one session, the config and binding files included | 4 MiB |
| Skill description | 1024 bytes, longer ones are cut and end with `…` |

## Reference

**Config** (`kind: Config`)

| Key | Required | Meaning |
|---|---|---|
| `apiVersion` | yes | `ctxtra/v1alpha1` |
| `kind` | yes | `Config` |
| `sources.<name>.trusted-path` | no | A folder of your own files. Absolute or starts with `~`. |
| `sources.<name>.git` | no | A team repository: `https://`, `ssh://` or `user@host:path`. |
| `sources.<name>.branch` | with `git` | The branch `sync` follows. |
| `sources.<name>.pinned-path` | no | Your clone, pinned to its committed `HEAD`. Absolute or starts with `~`. |
| `sources.<name>.commit` | no | With `git` or `pinned-path`: the accepted commit, a full ID in quotes. |
| `bindings` | no | List of binding files. Relative to the config's folder; `~` and `ctxtra://` work. |
| `budget.maxChars` | no | Integer ≥ 0, in characters. 0 or missing: no limit. |

**Binding** (`kind: Binding`)

| Key | Required | Meaning |
|---|---|---|
| `apiVersion` | yes | `ctxtra/v1alpha1` |
| `kind` | yes | `Binding` |
| `metadata.name` | yes | Letters, digits, `.`, `_`, `-`. Unique in the file. |
| `spec.selector` | yes | See [Selector](#selector). |
| `spec.files` | `files` or `skills` | Instruction files. |
| `spec.skills` | `files` or `skills` | Skill folders. |

**Environment**

| Variable | Default |
|---|---|
| `CTXTRA_CONFIG` | `~/.config/ctxtra/config.yaml` |
| `CTXTRA_LOG` | `~/.ctxtra/log.jsonl` |

Muse Code runs hooks with a cleared environment, so its sessions always use the defaults.

The session folder is the `cwd` the agent sends to the hook. If it is empty: the first `workspace_roots`
entry (Cursor), else the hook's working folder.
