# ctxtra

Add your own instruction files and skills to Claude Code, Codex CLI, Pi, OpenCode, Muse Code, Hermes Agent,
DeepSeek Harness (dsh), Cursor Agent CLI, GitHub Copilot CLI, Antigravity and Devin (Devin CLI, and Devin Desktop,
formerly Windsurf) sessions, for the folders and repositories you choose, in every worktree. Nothing is written into
repositories, and the repo's own `AGENTS.md` and your user-level agent files keep working as before.

**Status: preview (`v0`, config `ctxtra/v1`).** The config format may still change. ctxtra does
not check what the bound files say: bind only files you wrote or reviewed.

## How it works

`ctxtra setup` adds a session-start hook to each agent's user-level config. When a session starts, the
hook reads your config, finds the bindings that match the session folder, and gives the agent the bound
files and a list of the bound skills. Pi and OpenCode have no command hooks: for them, setup writes a small
extension (`~/.pi/agent/extensions/ctxtra.ts`, `~/.config/opencode/plugins/ctxtra.ts`) that runs ctxtra.
For Copilot, setup writes a hook file of its own, `~/.copilot/hooks/ctxtra.json`.
For Antigravity, setup adds a hook named `ctxtra` to `~/.gemini/config/hooks.json`, which the Antigravity
CLI (`agy`), App and IDE share.
For Devin, setup adds a hook to `~/.config/devin/config.json`, which Devin CLI and Devin Desktop share.
Hermes's shell hooks don't get the session folder, so setup writes a Python plugin (`~/.hermes/plugins/ctxtra/`)
that adds the text to the first message of each session.
DSH's command hooks need an extra package, so setup writes a plugin, `~/.dsh/ctxtra.mjs`, and adds the row that
loads it to `~/.dsh/cordis.patch.yml`, which applies to every profile.

At session start, Claude Code, Codex, Muse and Pi also show you a summary, for example:

```text
✓ ctxtra added 2 instructions and 1 skill
Instructions:
- english.md
- go-style.md
Skills:
- deploy
```

Each list shows up to 15 names, followed by `+N more` when needed. Muse may show fewer names to fit its message size limit. The summary also reports problems
found, if any. The session-start hooks of Cursor, Copilot and Antigravity, and OpenCode, Hermes and DSH plugins, can't
show a message.

## Install

```bash
go install ./cmd/ctxtra      # from a clone; puts the binary in $(go env GOPATH)/bin
ctxtra setup --dry-run       # show what would change
ctxtra setup                 # every agent found on this machine, or --agent claude --agent codex
```

`setup` adds hooks that run the binary you started it from, by its full path: agents started from the Dock
often don't have `~/go/bin` in their `PATH`. It changes only its own hook entries and backs up each file it
changes to `~/.ctxtra/backups`. Codex runs the hooks only after you trust them once (`/hooks` in Codex), and
asks once more after a hook changed, for example after an upgrade that changes the hooks.
Hermes loads the plugin only after you enable it once (`hermes plugins enable ctxtra`).
`ctxtra setup --remove` removes them.

An upgrade never needs `ctxtra setup` unless the release notes say so, 0.x releases included: each release
keeps serving what an earlier one set up ([setup compatibility](docs/spec.md#setup-compatibility)). When the
notes say so, Codex asks once to trust the changed hooks after setup, and Hermes needs the plugin reloaded, or
Hermes restarted, if it is running. If the binary moves, run `ctxtra setup` again, and trust the hooks again
in Codex; `doctor` shows which binary the hooks run and whether it exists. Every other command says so too: a
line on stderr per agent whose setup needs you (a missing or other binary, an older setup with less room, a
setup that is incomplete or gone, an agent found since the last setup), with the command that repairs it (see
[the spec](docs/spec.md#setup-notices)). Until then, Claude Code, Codex, Muse, Devin and Pi say at session
start that the binary is missing; the other agents add nothing, without a word.

## Let your agent help

ctxtra comes with a skill that teaches agents its commands, how to change the config safely, and the
risks of team repositories. `ctxtra setup` asks whether to install it; without a terminal, for example when
an agent runs setup, it doesn't ask. To install it at any time:

```bash
ctxtra skill           # the skill's files, exactly as the agents get them
ctxtra skill install   # writes it to ~/.ctxtra/skills/ctxtra, and adds a binding for it to your config
```

The binding is an ordinary one, named `ctxtra`, with `match: {global: true}`: remove it from your config to
stop adding the skill. Then ask any agent, for example "add my notes to every session in my work
repositories". The skill tells the agent to ask you before every change, and never to trust a repository or
accept a `sync` for you. Before setup, you can paste the output of `ctxtra skill` into an agent to start.

When your agents already have instruction files of their own, setup in a terminal then offers to move them
into ctxtra, so that all your agents get them. It asks only while no binding adds instructions, and only for
the agents that are set up. The files are the ones each agent reads at user level, checked against each agent
by the e2e tests:

| Agent | Files |
|---|---|
| Claude Code | `~/.claude/CLAUDE.md`, `~/.claude/rules/**/*.md` |
| Codex | `~/.codex/AGENTS.override.md`, `~/.codex/AGENTS.md` |
| Pi | the first of `~/.pi/agent/AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`, `CLAUDE.md`, `CLAUDE.MD` that exists; `APPEND_SYSTEM.md` |
| OpenCode | `~/.config/opencode/AGENTS.md` |
| Copilot | `~/.copilot/copilot-instructions.md`, `~/.copilot/instructions/**/*.instructions.md` |
| Antigravity | `~/.gemini/AGENTS.md`, `~/.gemini/GEMINI.md`, `~/.gemini/config/GEMINI.md` |
| Muse | `~/.config/muse/AGENTS.md` |
| DSH | `$DSH_HOME/AGENTS.md` |

Files that replace an agent's own system prompt or persona (Pi's `SYSTEM.md`, Hermes's `SOUL.md`) and files the
agent writes itself (Hermes's `memories/`) stay where they are. If you agree, setup copies the files to
`~/.ctxtra/backups/import-<time>-<n>/<agent>/` and starts an agent in `~/.ctxtra/import` with a prompt that lists
the files and the backup: the one agent with a CLI in PATH, or the one you pick. Every agent above except DSH,
whose app is a web page, can be started, and so can Hermes. The agent then follows the skill: it merges rules
that repeat, asks about rules that fit only one agent, shows you the new file, binds it, and empties the old
files only after that. It asks before each change, as usual. Without an agent to start, setup prints the
prompt for you to give an agent. Setup starts the agent with its normal permissions and never with flags that
skip them.

`ctxtra import` makes the same offer at any time, also when your config has bindings. `ctxtra import
--no-start` only backs up and lists the files: the skill runs it when you ask an agent to import your
instructions.

The skill is part of the binary: each version has a fixed text, which `ctxtra skill` shows. When you upgrade
ctxtra, the hook of the next session writes the new version to `~/.ctxtra/skills/ctxtra`. Antigravity asks before it reads the skill there (see [Limits](#limits)).

## Configure

`~/.config/ctxtra/config.yaml`:

```yaml
schema: ctxtra/v1
bindings:
  - name: me
    match:
      global: true
    instructions:
      - ~/notes/agents/me.md
  - name: work
    match:
      repos:
        - github.com/acme/**
    instructions:
      - ~/notes/agents/work.md
    skills:
      - ~/notes/skills/deploy
```

Or add a binding with a command. It keeps the rest of the config as you wrote it, and writes the binding
only when its files exist and are trusted:

```bash
ctxtra bind work --repo 'github.com/acme/**' --file ~/notes/agents/work.md --skill ~/notes/skills/deploy
```

Bindings can also live in their own files, which the config includes: your dotfiles, or a team repository.

Files in a git repository that has a remote are read only if you declare the repository as a source of
your own files. Its content may come from other people, so ctxtra skips it otherwise and says how to trust
it. Files outside git and in repositories without a remote are always read.

```yaml
sources:
  dotfiles:
    trusted-path: ~/dev/dotfiles
include:
  - ctxtra://dotfiles/ctxtra/work.yaml   # the same as ~/dev/dotfiles/ctxtra/work.yaml
```

A team repository can be added by URL. ctxtra keeps its own copy and serves the commit you accepted; a
new commit reaches your sessions after `ctxtra sync` shows you the diff and you accept it:

```bash
ctxtra add git@github.com:acme/team-ctx.git bindings/billing.yaml
ctxtra sync                 # review and accept new commits
ctxtra sync --restore       # a new laptop: fetch exactly the accepted commits
```

Full spec: [docs/spec.md](docs/spec.md). Use cases, from your own notes to a team repository you
contribute to: [docs/scenarios.md](docs/scenarios.md). `ctxtra help config` and `ctxtra help scenarios` show
them for the version you have.

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

- Each agent gets a limited amount of text in full: 360,000 characters in Claude Code and Muse, 1,000,000
  bytes in Codex, 650,000 bytes in Antigravity, 140,000 bytes in Copilot, 1,000,000 characters in the
  others (not measured in Cursor and Devin). Copilot counts tokens, and a byte is at most one token in any
  text, so bytes are a safe limit there; it costs English text, which would fit about four times more.
  Non-Latin text counts more in Codex, Antigravity and Copilot: a CJK character is 3 bytes; an emoji counts 2
  characters in Claude Code and Muse. Muse's hooks count bytes as they are in the hook's JSON answer, so
  Muse fits less non-ASCII text, and less text with many quotes or backslashes. Above the limit,
  whole files are dropped from the end, as with `budget.max-chars`; the message at session start, the log,
  `explain` and `doctor` name them. Several agents cut or drop a large hook output, so ctxtra splits the
  text into labeled parts there: 40 hooks per event in Claude Code and Muse, a plugin callback per part in
  Hermes, an injected step per part in Antigravity. See [the spec](docs/spec.md#capacity).
- Codex gets the text in full only from version 0.145.0, which reads the hook's `additionalContextLimit`;
  older versions show the model about 10,000 characters, and `doctor` warns.
- Resumed sessions don't get the text again, because the transcript already has it. `/compact` and
  `/clear` add it again in Claude and Codex. Copilot is different: it doesn't keep the text in the session,
  so its hook adds it again on resume. When Copilot compacts a long conversation, the text is gone for the
  rest of the session: Copilot doesn't run the hook again. Muse and OpenCode differ too, see below.
- A subagent gets the files when it starts, so it can see a newer version than its parent.
- Codex asks you to trust a hook again after every change to it, for example after the binary moved.
  `doctor` shows whether a trust record exists, but can't tell whether it is for the current hook.
- Muse Code (tested with 1.4.10) runs hooks with a cleared environment, so `CTXTRA_CONFIG` and `CTXTRA_LOG`
  don't reach the hook: Muse sessions use the default paths. A resumed Muse session runs the hook again, and
  Muse replaces the earlier text with the current version of the files.
- Devin: the hook goes into `~/.config/devin/config.json`, which Devin CLI and Devin Desktop's agent both
  read. Devin has no subagent hook. It allows comments in that file; `setup` leaves such a file alone. Devin
  also runs the hooks in `~/.claude/settings.json`: with both set up, Claude's hook adds nothing in Devin
  sessions (`doctor` shows it as skipped). Checked by the CLI tests only, not with the real agent, which
  needs a Devin login. Windsurf's old Cascade agent isn't supported: its hooks can't add context.
- Cursor: the Agent CLI waits for the session-start hook: with 800,000 characters, every codeword that
  the [test kit](e2e/cursorkit) samples from the text reached the model (tested with 2026.10.12). That
  shows the text arrived at those places, not that every line did. The desktop app (tested with 3.25)
  starts the hooks when the first message is sent and doesn't wait for them: text from hooks that end too
  late is lost for that message. So far every sampled codeword of 30,000 characters arrived, and all of
  100,000 characters was lost. It also runs the hooks in `~/.claude/settings.json`: with
  ctxtra's Cursor hook set up, Claude's hook adds nothing there, at once (`doctor` shows it as skipped).
- OpenCode: only OpenCode 2 (tested with 2.0.29); version 1 plugins have another format. OpenCode adds the
  text to every model request, not to the transcript, so resumed sessions get it too. Its background
  service keeps the plugin loaded: when the bound skills change, they apply from the second model request
  of the next session, or right away after `opencode service restart`.
- Antigravity: tested with the CLI (1.4.0); the App and IDE read the same hook file but aren't tested. A
  subagent is a session of its own there, so it gets the files from the same hook. Antigravity asks before
  it reads a file outside the workspace, such as a bound skill's `SKILL.md`, and headless runs (`agy -p`)
  refuse it. To skip the question, allow your skill folders, not everything, in
  `~/.gemini/antigravity-cli/settings.json`: `"permissions": {"allow": ["read_file(/Users/me/notes/skills)"]}`.
- Hermes: the plugin reads the session folder with `agent.runtime_cwd.resolve_agent_cwd()`, the way Hermes
  tells plugin authors to. With `plugins.isolation: host`, plugins run in another process, which may not
  know the session folder: then the plugin uses that process's folder, and folder and repository bindings
  can match the wrong place. The e2e tests pass with Hermes `v2026.9.24`, and with `main` at `bebe448`, which runs
  every session in one gateway.
- DSH: tested with 0.2.1-rc.2 (and 0.2.0-rc.2, the npm `latest`). The plugin uses DSH's plugin API, which may still
  change before 1.0. DSH adds the text after the first prompt and the repo's `AGENTS.md`; `/clear` and
  compaction add it again.

## Development

```bash
go test ./...             # unit tests and CLI tests; each uses its own temporary HOME; the Pi, OpenCode and DSH tests also need node, the Hermes test python3
cd e2e && go test -count=1 ./...   # real agents against a mock model; see e2e/README.md
```

The CLI tests in `cmd/ctxtra/testdata/script` run the built binary: `setup`, the inspection commands,
and the hook commands that `setup` writes, the way an agent runs them. Run one with
`go test ./cmd/ctxtra -run TestScript/<name>`.

The contract tests keep upgrades free of `ctxtra setup` (see [the spec](docs/spec.md#setup-compatibility)):
`internal/setup/contract_test.go` compares what setup writes with `internal/setup/testdata/contract`, and
`internal/cli/contract_test.go` runs the hooks of every setup in `testdata/clients` that a version wrote.

Layout:

- `cmd/ctxtra`: the binary.
- `skills/ctxtra`: the ctxtra skill. `embed.go` embeds it and the docs into the binary (`ctxtra skill`,
  `ctxtra help`).
- `internal/config`: loads and validates the config and binding files, and matches `match` blocks.
- `internal/resolve`: decides what a session gets and renders the text.
- `internal/chunk`: splits the text into labeled parts for agents that cut a large hook output.
- `internal/agent`: the `Agent` interface. One package per agent (`antigravity`, `claude`, `codex`, `copilot`,
  `cursor`, `devin`, `dsh`, `hermes`, `muse`, `opencode`, `pi`) with its hook setup and answer format; `hookjson`
  edits the JSON hook files they share; `extfile` writes the extension files of Pi, OpenCode, Hermes and DSH;
  `registry` lists them.
  To add an agent, implement `agent.Agent` and add it to the registry.
- `internal/hook`: the entry point the agents run, and the log.
- `internal/setup`: finds the binary and runs each agent's setup.
- `internal/cli`: the commands.
- `internal/source`: fetches pinned sources with git and keeps their accepted commits in the store.
- `internal/trust`: decides whether a file on disk may be read (outside git, no remote, or a trusted source).
- Helpers: `gitrepo`, `fsutil` (bounded, non-blocking reads and atomic writes), `jsonorder`, `paths`,
  `testenv` (tests only).
