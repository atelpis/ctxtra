# ctxtra guide

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
brew install atelpis/tap/ctxtra   # or: go install github.com/atelpis/ctxtra/cmd/ctxtra@latest
ctxtra setup --dry-run            # show what would change
ctxtra setup                      # every agent found on this machine, or --agent claude --agent codex
```

`setup` adds hooks that run the binary you started it from, by its full path: agents started from the Dock
often don't have `~/go/bin` in their `PATH`. It changes only its own hook entries and backs up each file it
changes to `~/.ctxtra/backups`. Codex runs the hooks only after you trust them once (`/hooks` in Codex), and
asks once more after a hook changed, for example after an upgrade that changes the hooks.
Hermes loads the plugin only after you enable it once (`hermes plugins enable ctxtra`).
`ctxtra setup --remove` removes them.

An upgrade never needs `ctxtra setup` unless the release notes say so, 0.x releases included: each release
keeps serving what an earlier one set up ([setup compatibility](spec.md#setup-compatibility)). When the
notes say so, Codex asks once to trust the changed hooks after setup, and Hermes needs the plugin reloaded, or
Hermes restarted, if it is running. If the binary moves, run `ctxtra setup` again, and trust the hooks again
in Codex; `doctor` shows which binary the hooks run and whether it exists. Every other command says so too: a
line on stderr per agent whose setup needs you (a missing or other binary, an older setup with less room, a
setup that is incomplete or gone, an agent found since the last setup), with the command that repairs it (see
[the spec](spec.md#setup-notices)). Until then, Claude Code, Codex, Muse, Devin and Pi say at session
start that the binary is missing; the other agents add nothing, without a word.

## Let your agent help

ctxtra comes with a skill that teaches agents its commands, how to change the config safely, and the
risks of team repositories. `ctxtra setup` prints the command that installs it:

```bash
ctxtra skill show ctxtra      # the skill's files, exactly as the agents get them
ctxtra skill install ctxtra   # writes it to ~/.ctxtra/skills/ctxtra, and adds a binding for it to your config
```

The binding is an ordinary one, named `ctxtra`, with `match: {global: true}`: `ctxtra binding remove ctxtra`
stops adding the skill. Then ask any agent, for example "add my instructions to every session in my work
repositories". The skill tells the agent to ask you before every change, and never to trust a repository or
accept a new commit for you. Before setup, you can paste the output of `ctxtra skill show ctxtra` into an
agent to start.

When your agents already have instruction files of their own, `ctxtra import` moves them into ctxtra, so that
all your agents get them; setup names it while no binding adds instructions. It takes the files of the
agents that are set up: the ones each agent reads at user level, checked against each agent by the e2e
tests:

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
agent writes itself (Hermes's `memories/`) stay where they are. `ctxtra import` copies the files to
`~/.ctxtra/backups/import-<time>-<n>/<agent>/` and lists them. `ctxtra import --start claude` then starts that
agent in `~/.ctxtra/import`, in your terminal, with a prompt that lists the files and the backup. Every agent
above except DSH, whose app is a web page, can be started, and so can Hermes. The agent follows the skill
(install it first): it merges rules that repeat, asks about rules that fit only one agent, shows you the new
file, and binds it. Then it recommends removing the moved rules from the old files, so that your agents don't
get two copies that drift apart, and does it only if you agree. It asks before each change, as usual. Without
an agent to start, import prints the prompt for you to give an agent. It starts the agent with its normal
permissions and never with flags that skip them. When you ask an agent to import your instructions, the skill
runs `ctxtra import` and does the rest itself.

The skill is part of the binary: each version has a fixed text, which `ctxtra skill show ctxtra` shows. When you upgrade
ctxtra, the hook of the next session writes the new version to `~/.ctxtra/skills/ctxtra`, and the same for
ctxtra's other skills that you installed. Antigravity asks before it reads the skill there (see [Limits](#limits)).

## Configure

`~/.config/ctxtra/config.yaml`:

```yaml
schema: ctxtra/v1
bindings:
  - name: me
    match:
      global: true
    instructions:
      - ~/agent-context/me.md
  - name: work
    match:
      repos:
        - github.com/acme/**
    instructions:
      - ~/agent-context/work.md
    skills:
      - ~/agent-context/skills/deploy
```

Or add a binding with a command. It keeps the rest of the config as you wrote it, and writes the binding
only when its files exist and are trusted:

```bash
ctxtra binding add work --repo 'github.com/acme/**' --instruction ~/agent-context/work.md --skill ~/agent-context/skills/deploy
```

Bindings can also live in their own files, which the config includes: your dotfiles, or a team repository.

Files in a git repository that has a remote are read only if you declare the repository as a source of
your own files. Its content may come from other people, so ctxtra skips it otherwise and says how to trust
it. Files outside git and in repositories without a remote are always read.

```bash
ctxtra source add trusted-path ~/dev/dotfiles --include ctxtra/work.yaml
```

```yaml
sources:
  dotfiles:
    trusted-path: ~/dev/dotfiles
include:
  - ctxtra://dotfiles/ctxtra/work.yaml   # the same as ~/dev/dotfiles/ctxtra/work.yaml
```

A team repository is added by URL. ctxtra keeps its own copy and serves the commit you accepted; a new
commit reaches your sessions after `ctxtra source update` shows you the diff and you accept it, in your
terminal:

```bash
ctxtra source add git git@github.com:acme/team-ctx.git --include bindings/billing.yaml
ctxtra source update           # review and accept new commits
ctxtra source update --check   # review only: for scripts and agents
ctxtra source restore          # a new laptop: fetch exactly the accepted commits
```

`ctxtra source list`, `ctxtra binding-file list` and `ctxtra binding list` show what the config has; each
group has `remove` too.

Full spec: [spec.md](spec.md). Use cases, from your own instruction files to a team repository you
contribute to: [scenarios.md](scenarios.md). `ctxtra help config` and `ctxtra help scenarios` show
them for the version you have.

## Check

```bash
ctxtra context files [DIR]     # files and skills a session in DIR gets; problems on stderr
ctxtra context show [DIR]      # the exact text the agent gets
ctxtra context explain [DIR]   # every binding: matched or not, and why
ctxtra doctor [DIR]            # config problems, setup state, Codex trust, recent hook runs
```

They exit with 1 when a session wouldn't get everything it should, and take `--json` (except
`context show`). `ctxtra help` lists every command; `ctxtra help COMMAND` shows one.

Every hook run is logged to `~/.ctxtra/log.jsonl`. The hook never blocks a session: on any error it adds
nothing and logs the error.

## Follow the agent to another folder

A session gets what its session folder matches when it starts. When you then ask the agent to also change
something in another repository ("update the client in ../billing too"), your bindings for that repository
don't reach the session. ctxtra has an optional skill for this:

```bash
ctxtra skill install ctxtra-follow
```

Before the agent works in a folder outside the session folder, the skill tells it to run
`ctxtra context show DIR --exclude-session SESSION_DIR --part 1`, once for each folder: the top of another repository, or a
project folder in a repository with several. That prints only the files and skills the folder adds, not
what the session has already (a file the session has from another folder is listed by name, so the agent
knows it applies here too), and the agent follows them for its work there. Each file's header names the
repository or folder it is for. A long text comes in outputs of at most 8,000 bytes, each saying how to get
the next, because agents cut long command output.

Unlike session start, this depends on the model: it decides when to use the skill, and it can miss it. The
agent gets only your own bound files, read with the same trust rules as at session start. A binding for a
folder below the one the agent ran the command for doesn't reach it. That includes folders under the
session folder: a binding for `~/dev/mono/billing/**` doesn't reach a session started in `~/dev/mono`.

## Limits

- Each agent gets a limited amount of text in full: 360,000 characters in Claude Code and Muse, 1,000,000
  bytes in Codex, 650,000 bytes in Antigravity, 140,000 bytes in Copilot, 1,000,000 characters in the
  others (not measured in Cursor and Devin). Copilot counts tokens, and a byte is at most one token in any
  text, so bytes are a safe limit there; it costs English text, which would fit about four times more.
  Non-Latin text counts more in Codex, Antigravity and Copilot: a CJK character is 3 bytes; an emoji counts 2
  characters in Claude Code and Muse. Muse's hooks count bytes as they are in the hook's JSON answer, so
  Muse fits less non-ASCII text, and less text with many quotes or backslashes. Above the limit,
  whole files are dropped from the end, as with `budget.max-chars`; the message at session start, the log,
  `context explain` and `doctor` name them. Several agents cut or drop a large hook output, so ctxtra splits the
  text into labeled parts there: 40 hooks per event in Claude Code and Muse, a plugin callback per part in
  Hermes, an injected step per part in Antigravity. See [the spec](spec.md#capacity).
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
  the [test kit](../e2e/cursorkit) samples from the text reached the model (tested with 2026.10.12). That
  shows the text arrived at those places, not that every line did. The desktop app (tested with 3.25)
  starts the hooks when the first message is sent and doesn't wait for them: text from hooks that end too
  late is lost for that message. So far every sampled codeword of 30,000 characters arrived, and all of
  100,000 characters was lost. It also runs the hooks in `~/.claude/settings.json`: with
  ctxtra's Cursor hook set up, Claude's hook adds nothing there, at once (`doctor` shows it as skipped).
- OpenCode: only OpenCode 2 (tested with 2.0.29); version 1 plugins have another format. OpenCode adds the
  text to every model request, not to the transcript, so resumed sessions get it too. Its background
  service keeps the plugin loaded: when the bound skills change, they apply from the second model request
  of the next session, or right away after `opencode service restart`.
- Antigravity: tested with the CLI (1.4.7); the App and IDE read the same hook file but aren't tested. A
  subagent is a session of its own there, so it gets the files from the same hook. Antigravity asks before
  it reads a file outside the workspace, such as a bound skill's `SKILL.md`, and headless runs (`agy -p`)
  refuse it. To skip the question, allow your skill folders, not everything, in
  `~/.gemini/antigravity-cli/settings.json`: `"permissions": {"allow": ["read_file(/Users/me/agent-context/skills)"]}`.
- Hermes: the plugin reads the session folder with `agent.runtime_cwd.resolve_agent_cwd()`, the way Hermes
  tells plugin authors to. With `plugins.isolation: host`, plugins run in another process, which may not
  know the session folder: then the plugin uses that process's folder, and folder and repository bindings
  can match the wrong place. The e2e tests pass with Hermes `v2026.9.24`, and with `main` at `bebe448`, which runs
  every session in one gateway.
- DSH: tested with 0.2.1-rc.2 (and 0.2.0-rc.2, the npm `latest`). The plugin uses DSH's plugin API, which may still
  change before 1.0. DSH adds the text after the first prompt and the repo's `AGENTS.md`; `/clear` and
  compaction add it again.
