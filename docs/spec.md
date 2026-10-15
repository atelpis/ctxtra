# Config spec: ctxtra/v1alpha1

> **Preview.** `v1alpha1` may change without a migration path. ctxtra does not protect you from the
> content of the files you bind: an instruction file or skill is acted on like anything you type to the
> agent. Bind only files you wrote or reviewed.

## Terms

- **Session folder:** the folder an agent session starts in.
- **Binding:** a document of kind `Binding`. It says: in sessions that match this selector, add these
  instruction files and these skills.
- **Binding file:** a YAML file with one or more bindings, separated by `---`.
- **Config:** the file that lists which binding files you use.

Instruction files are plain Markdown. Skills are folders with a `SKILL.md`, the same format other agent
tools use. Neither has anything specific to ctxtra.

## Config

`~/.config/ctxtra/config.yaml`, or the file in `$CTXTRA_CONFIG`.

```yaml
apiVersion: ctxtra/v1alpha1
kind: Config

include:                              # binding files, applied in this order
  - bindings/personal.yaml            # relative to this file's folder
  - ~/dev/acme-context/bindings/go.yaml

budget:
  maxChars: 40000                     # optional; 0 or missing = no limit
```

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
- Each path is absolute, starts with `~`, or is relative to the binding file's folder.
- Globs are not supported: list each file and skill folder by name.
- Each file is added once per session, at its first position, and the same for each skill. Two skills with
  the same `name`: the first is used, the other is reported.
- `SKILL.md` must start with YAML frontmatter that has a `description`. `name` is optional (default: the
  folder name). Other keys are ignored.
- Claude Code, Codex and Cursor get the file contents and a list of the skills (name, description, path),
  and read a `SKILL.md` when a task needs it. Pi gets files as context files and skills as native skills.

## Order and budget

Bindings apply in config order: the `include` list, then the documents of each file in order.

With `budget.maxChars`, whole files are dropped from the end until the added text fits. The budget counts
characters (Unicode code points), not bytes, and covers the whole added text, headers included. The skill
list is never dropped. `ctxtra explain` shows what was dropped.

## Validation

Problems never make ctxtra add more than the config says:

- A config with a wrong `apiVersion` or `kind`, an invalid `budget`, or broken YAML, is ignored as a whole.
- A broken binding is skipped. The other bindings in the same file still apply. This includes a document
  that isn't a map or uses anchors. A YAML syntax error skips the whole file, because the parser can't tell
  where the next document starts.
- Unknown keys, duplicate keys, null values, empty lists, YAML anchors and aliases are errors.
- A missing file is reported. The session still starts.

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
| `include` | no | List of binding files. Relative to the config's folder; `~` works. |
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
