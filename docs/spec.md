# Config spec: ctxtra/v1alpha1

> **Preview.** `v1alpha1` may change without a migration path. ctxtra does not protect you from the
> content of the files you bind: an instruction file or skill is acted on like anything you type to the
> agent. Bind only files you wrote or reviewed.

## Terms

- **Session folder:** the folder an agent session starts in.
- **Binding:** a document of kind `Binding`. It says: in sessions that match this selector, add these
  instruction files and these skills.
- **Binding file:** a YAML file with one or more bindings, separated by `---`.
- **Config:** the file that lists which binding files you use, and declares your sources.
- **Source:** a folder of your own files, declared in the config. Files in it are referenced as
  `ctxtra://<source>/<path>`.

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

bindings:                             # binding files, applied in this order
  - bindings/personal.yaml            # relative to this file's folder
  - ctxtra://dotfiles/ctxtra/go.yaml  # a file in the dotfiles source

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

A source has a name (letters, digits, `.`, `_`, `-`) and exactly one kind. `trusted-path` is the only kind
so far.

`ctxtra://<source>/<path>` works in `bindings` and in `files` and `skills` of any binding file. It is the
same as the path under the source's folder. The path as written must stay inside the source: `..` that
leaves it, and absolute paths, are errors. A symlink in the source is followed by the rules above, so it
can lead outside the source, but only to a file that is trusted on its own.

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
- Claude Code, Codex and Cursor get the file contents and a list of the skills (name, description, path),
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

The session folder is the `cwd` the agent sends to the hook. If it is empty: the first `workspace_roots`
entry (Cursor), else the hook's working folder.
