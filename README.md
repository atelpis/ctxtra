<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hero-dark.svg">
    <img src="docs/assets/hero-light.svg" alt="ctxtra: context for coding agents, keyed by git repo. Nothing written into your repos, nothing to set up per worktree" width="100%">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/atelpis/ctxtra/actions/workflows/ci.yml"><img src="https://github.com/atelpis/ctxtra/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/atelpis/ctxtra/actions/workflows/e2e.yml"><img src="https://github.com/atelpis/ctxtra/actions/workflows/e2e.yml/badge.svg" alt="e2e against real agents"></a>
  <img src="https://img.shields.io/github/go-mod/go-version/atelpis/ctxtra" alt="Go version">
  <img src="https://img.shields.io/badge/status-preview-F59E0B" alt="Status: preview">
</p>

Keep your agent instructions and skills in your own folder or in a team repo, and choose which repos get
them. ctxtra adds them when a session starts, in every clone and worktree, in Claude Code, Codex, Copilot CLI
and eight more agents. Nothing is written into your repos.

It's for you if:

- you have notes for a repo that you can't or don't want to commit, like a client's repo or your team's,
- you run agents in worktrees, and a new worktree starts without your `CLAUDE.local.md`,
- you use more than one agent, and keep copying text between `CLAUDE.md` and `AGENTS.md` (setup offers
  to merge them into one file),
- your team keeps the same conventions in dozens of repos and updates them with PRs to each one.

```bash
go install github.com/atelpis/ctxtra/cmd/ctxtra@latest
ctxtra setup --dry-run    # lists each agent config it would change
ctxtra setup              # or only some agents: --agent claude --agent codex
```

`setup` adds its hooks to the agents' user-level configs, and `ctxtra setup --remove` takes out only those.
Your repos, their `AGENTS.md` and your skill folders stay as they are.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/story-dark.svg">
    <img src="docs/assets/story-light.svg" alt="Out of the box, one agent reads its own files. Generators write each agent's files into your project and need a rerun and a new diff after every change. ctxtra hooks into every agent and adds files from your own folder or a pinned team repo, bound to each project, with nothing written into the projects" width="100%">
  </picture>
</p>

## Bind your files, don't copy them

Generators write each agent's files into your repo. If you commit them, every edit means a rerun and a
generated diff, and your personal notes end up in a shared repo. If you don't, every new clone and worktree
needs a rerun. ctxtra leaves your files where they are and binds them instead:

```yaml
bindings:
  - name: go
    match:
      dirs: ['~/dev/**']              # every repo under ~/dev
      repos: ['github.com/acme/**']   # every acme repo, wherever it's cloned
    instructions: [~/notes/go-style.md]
    skills: [~/notes/skills/review]
```

- Match by folder, by git remote, or by one folder of a monorepo. Worktrees match too, wherever they are on
  disk, including the one your agent created a minute ago.
- Your Markdown, your config and your code can each live in a different place: a notes folder, your
  dotfiles, a team repo on GitHub.
- Bind instructions and skills the same way. Globs work too: `~/notes/**/*.md`.
- The agents' own `AGENTS.md` and `CLAUDE.md` files keep working. ctxtra only adds to them.
- Each session gets the files for the folder it starts in. If the agent then works in another repo, the
  optional `ctxtra-follow` skill tells it how to get that repo's files, but like any skill, the model can
  miss it.

<p align="center">
  <img src="docs/assets/demo.svg" alt="Four agent sessions in four folders get four different sets of files from one config" width="100%">
</p>

## In context from the first message

A skill works only when the model decides it fits the task, and sometimes it doesn't. Rules that must
always apply belong in the context from the start. ctxtra adds your bound instructions when the session
starts, chosen by folder and repo, not by the model. Bound skills are listed next to them, for the tasks
that need them.

Only what you bind gets in. `ctxtra show DIR` prints the exact text a session in `DIR` gets, and
`ctxtra explain DIR` shows why each binding matched or didn't.

## Share packages with your team

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/team-sync-dark.svg">
    <img src="docs/assets/team-sync-light.svg" alt="A team repository is pinned to a commit; new commits reach your sessions only after you accept them in ctxtra sync" width="100%">
  </picture>
</p>

A package is a git repo of Markdown files and skills, with bindings that say where they go. The team writes
"billing.md goes to every session in github.com/acme/billing" once, and everyone who adds the package gets
it there, with no PRs to the billing repo:

```bash
ctxtra add git@github.com:acme/team-ctx.git bindings/billing.yaml
```

Each person stays at the commit they accepted, like a lockfile. `ctxtra sync` shows the diff of a new commit
and warns when a binding starts to match more sessions. A package's bindings can point only to its own
files, never to `~/.ssh` or anything else on your disk. Its text still goes to your agent, though, and
`ctxtra add` accepts the current commit, so read a package before you add it, as you would read code before
you run it.

## Your agent sets it up, you approve

You don't need to learn the config. ctxtra comes with a skill that teaches your agents how to use it. Ask
"add my Go notes to every session in my work repos": the agent shows you the change and asks before it
writes anything. It never trusts a repo or accepts a team update for you.

Already have a `~/.claude/CLAUDE.md` or `~/.codex/AGENTS.md`? `setup` can move them into one file that all
your agents get, if you want it to. An agent merges them and shows you the result, and the old files change
only if you agree.

## Works with your agents

Claude Code · Codex CLI · GitHub Copilot CLI · Cursor Agent CLI · OpenCode · Pi · Antigravity · Devin ·
Hermes Agent · Muse Code · DeepSeek Harness

Only use Claude Code? You still get notes per repo that you don't have to commit, and the same files in every
worktree. The tests run nine of these agents' real binaries against a mock model and check what reaches
it, and a nightly job runs them against the latest releases. You keep running plain `claude` or `codex`.

## Quick start

After `ctxtra setup`, ask your agent, or bind a file yourself. For example, keep your notes for a repo in
`~/notes/billing.md` instead of a `CLAUDE.local.md`, and bind them to the repo's remote, so every worktree
gets them:

```text
$ ctxtra bind billing --repo github.com/acme/billing --file ~/notes/billing.md
added binding billing to ~/.config/ctxtra/config.yaml: for github.com/acme/billing
  instructions: ~/notes/billing.md

$ cd ~/wt/billing-fix && claude
✓ ctxtra added 1 instruction
Instructions:
- billing.md
```

## Docs

- [Guide](docs/guide.md): every command, how each agent gets the text, and the limits.
- [Scenarios](docs/scenarios.md): step by step, from your own notes to a team repo.
- [Config spec](docs/spec.md).

ctxtra is in preview, and the config format may still change. It runs on macOS and Linux.
