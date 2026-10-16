# ctxtra

Add your own instruction files and skills to Claude Code, Codex CLI, Pi, OpenCode and Cursor Agent CLI sessions, for
the folders and repositories you choose, in every worktree. Nothing is written into repositories, and the
repo's own `AGENTS.md` and your user-level agent files keep working as before.

**Status: preview (`v0`, config `ctxtra/v1alpha1`).** The config format may still change. ctxtra does
not check what the bound files say: bind only files you wrote or reviewed.

## How it works

`ctxtra setup` adds a session-start hook to each agent's user-level config. When a session starts, the
hook reads your config, finds the bindings that match the session folder, and gives the agent the bound
files and a list of the bound skills. Pi and OpenCode have no command hooks: for them, setup writes a small
extension (`~/.pi/agent/extensions/ctxtra.ts`, `~/.config/opencode/plugins/ctxtra.ts`) that runs ctxtra.

At session start, Claude Code, Codex and Pi also show you a one-line summary, for example
`ctxtra: added 2 files (english.md, go-style.md) and 1 skill (deploy)`, and the number of problems
found, if any. Cursor's session-start hook and OpenCode plugins can't
show a message.

## Install

```bash
go install ./cmd/ctxtra      # from a clone; puts the binary in $(go env GOPATH)/bin
ctxtra setup --dry-run       # show what would change
ctxtra setup                 # every agent found on this machine, or --agent claude --agent codex
```

`setup` adds hooks that run the binary you started it from, by its full path: agents started from the Dock
often don't have `~/go/bin` in their `PATH`. It changes only its own hook entries and backs up each file it
changes to `~/.ctxtra/backups`. Codex runs the hooks only after you trust them once (`/hooks` in Codex).
`ctxtra setup --remove` removes them.

An upgrade in the same place needs no new setup. If the binary moves, run `ctxtra setup` again, and trust
the hooks again in Codex; `doctor` shows which binary the hooks run and whether it exists.

## Configure

`~/.config/ctxtra/config.yaml`:

```yaml
apiVersion: ctxtra/v1alpha1
kind: Config
include:
  - bindings.yaml
```

`~/.config/ctxtra/bindings.yaml`:

```yaml
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata:
  name: me
spec:
  selector:
    global: true
  files:
    - ~/notes/agents/me.md
---
apiVersion: ctxtra/v1alpha1
kind: Binding
metadata:
  name: work
spec:
  selector:
    gitRepos:
      - remote: github.com/acme/**
  files:
    - ~/notes/agents/work.md
  skills:
    - ~/notes/skills/deploy
```

Full spec: [docs/spec.md](docs/spec.md).

## Check

```bash
ctxtra files [DIR]      # files and skills a session in DIR gets; problems on stderr
ctxtra show [DIR]       # the exact text the agent gets
ctxtra explain [DIR]    # every binding: matched or not, and why
ctxtra doctor [DIR]     # config problems, setup state, Codex trust, recent hook runs
```

Every hook run is logged to `~/.ctxtra/log.jsonl`. The hook never blocks a session: on any error it adds
nothing and logs the error.

## Limits

- Claude, and Codex by default, show the model only a preview of large hook output (about 10,000
  characters). `explain` and `doctor` warn above that.
- Resumed sessions don't get the text again, because the transcript already has it. `/compact` and
  `/clear` add it again in Claude and Codex.
- A subagent gets the files when it starts, so it can see a newer version than its parent.
- Codex asks you to trust a hook again after every change to it, for example after the binary moved.
  `doctor` shows whether a trust record exists, but can't tell whether it is for the current hook.
- OpenCode: only OpenCode 2 (tested with 2.0.29); version 1 plugins have another format. OpenCode adds the
  text to every model request, not to the transcript, so resumed sessions get it too. Its background
  service keeps the plugin loaded: when the bound skills change, they apply from the second model request
  of the next session, or right away after `opencode service restart`.

## Development

```bash
go test ./...             # unit tests and CLI tests; each uses its own temporary HOME; the Pi and OpenCode tests also need node
cd e2e && go test -count=1 ./...   # real agents against a mock model; see e2e/README.md
```

The CLI tests in `cmd/ctxtra/testdata/script` run the built binary: `setup`, the inspection commands,
and the hook commands that `setup` writes, the way an agent runs them. Run one with
`go test ./cmd/ctxtra -run TestScript/<name>`.

Layout:

- `cmd/ctxtra`: the binary.
- `internal/config`: loads and validates the config and binding files, and matches selectors.
- `internal/resolve`: decides what a session gets and renders the text.
- `internal/agent`: the `Agent` interface. One package per agent (`claude`, `codex`, `cursor`, `opencode`,
  `pi`) with its hook setup and answer format; `hookjson` edits the JSON hook files they share; `extfile`
  writes the extension files of Pi and OpenCode; `registry` lists them.
  To add an agent, implement `agent.Agent` and add it to the registry.
- `internal/hook`: the entry point the agents run, and the log.
- `internal/setup`: finds the binary and runs each agent's setup.
- `internal/cli`: the commands.
- Helpers: `gitrepo`, `fsutil` (bounded, non-blocking reads and atomic writes), `jsonorder`, `paths`,
  `testenv` (tests only).
