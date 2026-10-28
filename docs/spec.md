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
- **Source:** a folder of your own files (trusted), or a team git repository pinned to a commit you
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

A `git` source is a team repository. ctxtra keeps its own copy, so you don't need a clone, and serves one
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
  team repository before you add it. `source add git` shows its bindings and its executable files.
- Accepting a commit accepts **every file in it**, also files the review lists only by name: a file you
  reference later is used at once, without another review.
- Anyone who can edit your config can choose the version, for example through a dotfiles repository that
  others push to. A `commit` copied from another laptop was reviewed there.
- A binding file in a pinned source may only reference files in the same source: relative paths, or
  `ctxtra://` with its own name. Absolute paths, `~`, `..` that leaves the source and other sources are
  errors, and the binding is skipped. A team repository can't add your files to a session, and can't
  declare sources or include other files: only your config does.
- Files are read from ctxtra's store (below), never from a clone. Symlinks and submodules in the
  repository are left out.
- ctxtra doesn't check for new commits by itself: sessions never fetch. Run `ctxtra source update` regularly.

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

Every `source add` takes `--name NAME` (default: the repository or folder name), `--include PATH` to add
binding files of the source to `include` in the same edit (repeatable), and `--dry-run`, which checks and
fetches nothing. Without `--include`, it lists the binding files in the source.

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
  `ctxtra://` references to the pinned source stay pinned. Use this to edit the team repository and see
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

### Review

```text
$ ctxtra source update
team-ctx: abc1234 -> def5678 (main, git@github.com:acme/team-ctx.git)
warning: binding billing in bindings/billing.yaml now applies to every session (was: github.com/acme/billing)
diff --git a/bindings/billing.yaml b/bindings/billing.yaml
...
also changed, not used by you:
  changed  README.md
  added    tools/setup.sh  executable
Accepting accepts these files too: a file you reference later is used without review. ctxtra source update --full shows them.
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
- The new commit is written to the store before the review. A commit over the limits fails there.

`source update` asks only when stdin and stdout are a terminal, so you see what you accept; the question
goes to stderr. Without a terminal, or with `--no-input`, it fails before it fetches, and `--check` does
the fetch and the review without asking. There is no option that accepts without asking.

When you answer `N`, ctxtra remembers the commit as dismissed. Sessions that use the source,
`context files` and `doctor` say `def5678 not accepted (dismissed)`. The next `source update` shows the
review again. Input that ends without an answer decides nothing: no commit is accepted or dismissed.

### Notices

Sessions never fetch, so they can't know about new commits. When a session uses a pinned source that
wasn't checked for 7 days, it says `team-ctx last checked 12 days ago; run ctxtra source update to check
for changes` (Claude Code, Codex, Muse and Pi show it at session start, and `context files` and `doctor`
show it too). A check is a fetch of the branch that worked, by `source add` or `source update`. A source
that was never checked on this machine, for example after `source restore` on a new laptop, says so.

When a new commit doesn't continue the accepted one, `source update` warns: the branch was force-pushed.
Accepting it is still your choice.

The commands that change the config change it as text: they replace the `commit` value or add lines, and keep your
comments and layout. In a flow-style map (`{...}`) they write the file again from its YAML, which keeps
comments but not the layout. A changed config is checked before it is written, written atomically, and the
old one is kept in `~/.ctxtra/backups`. A symlinked config stays a symlink. If the source changed in the
config while `source update` waited for your answer, nothing is accepted.

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
- `~/.ctxtra/state.json`: per source, the last check and the dismissed commit. Losing it loses the notices.
- `~/.ctxtra/mappings.json`: the folders of clones added as git sources (above). `~/.ctxtra/configs.json`:
  the config files that ctxtra commands changed.

### How ctxtra runs git

- It runs your `git`, so ssh keys, the ssh agent and credential helpers work. Only `source add`,
  `source update` and `source restore` run git, and git may prompt. Sessions never fetch.
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
  and read a `SKILL.md` when a task needs it. Pi gets instruction files as context files and skills as native skills.
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

After every command that runs, but `hook`, `setup`, `onboard`, `doctor`, `help`, `version` and `completion`, ctxtra
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

- A config with a wrong `schema`, an invalid `budget`, source or `include`, or broken YAML, is ignored as a
  whole.
- A broken binding is skipped, in the config or in a binding file. The other bindings in the same file
  still apply.
- A binding file that can't be read as a whole is skipped as a whole: a wrong `schema`, a key other than
  `schema` and `bindings`, a YAML syntax error, more than one YAML document, or YAML anchors.
- Unknown keys, duplicate keys, null values, empty lists, YAML anchors and aliases are errors.
- A missing file is reported. The session still starts.
- A file that isn't trusted (see [Sources and trust](#sources-and-trust)) is skipped and reported.

`ctxtra doctor` lists every problem.

## Command line

`ctxtra help` lists the commands, and `ctxtra help COMMAND` (the same as `ctxtra COMMAND --help`) shows one
with its flags and examples. The commands that change the config are grouped by what they change:
`source`, `binding-file` and `binding`, each with `add`, `list` and `remove`.

- Exit codes: 0 when the command did what it was asked; 1 when it failed, or when `doctor` or a `context`
  command found that a session wouldn't get everything it should; 2 for a usage error (an unknown command
  or flag, a missing argument), with the command's usage hint on stderr.
- Results go to stdout; problems, warnings, notices and questions go to stderr. In a report (`doctor`,
  `context explain`, the review of `source update`), its problems and warnings are part of the result, on
  stdout.
- `--json` (on `doctor`, `context files`, `context explain`, the `list` commands, `source show` and
  `source update --check`) prints one JSON object: `schema_version`, `data`, and `diagnostics`, a list of
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
| `sources.<name>.git` | no | A team repository: `https://`, `ssh://` or `user@host:path`. |
| `sources.<name>.branch` | with `git` | The branch `source update` follows. |
| `sources.<name>.local-git` | no | Your clone, pinned to its committed `HEAD`. Absolute or starts with `~`. |
| `sources.<name>.commit` | no | With `git` or `local-git`: the accepted commit, a full ID in quotes. |
| `bindings[]` | no | Your own bindings, see **Binding** below. Paths are relative to the config's folder. |
| `include[]` | no | Binding files. Relative to the config's folder; `~` and `ctxtra://` work. |
| `budget.max-chars` | no | Integer ≥ 0, in characters. 0 or missing: no limit. |

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
