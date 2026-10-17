# Agent contract tests

These tests run real agents (Claude Code, Codex CLI, Pi, OpenCode, GitHub Copilot CLI, Muse Code) with ctxtra set
up, and check what reaches the model. The model is a mock on `127.0.0.1` ([mock/](mock)) that records every request and answers with
text or a scripted tool call, so the tests are free and deterministic.

Every run gets its own home folder, with the agents' config folders inside it, a minimal environment and
a dummy API key. Your agent configs, logins and history are never read or changed; the tests fail if
your real agent config files change while they run.

This folder is a separate Go module: `go test ./...` at the repository root doesn't run it, and
`go install` of ctxtra doesn't download it.

## Run

```bash
cd e2e
go test -count=1 ./...                      # every agent found
go test -count=1 . -agent claude            # one agent
go test -count=1 ./... -run TestSkills -v   # one scenario, with each agent's version and timing
go test -count=1 ./... -keep -v             # keep each run's home folder, and print where it is
```

Always pass `-count=1`. The test cache doesn't know about ctxtra's sources (another module, built by
the tests) or the agent binaries, so a cached pass can hide a change in either.

With `CTXTRA_E2E_EVIDENCE=DIR`, a failed run saves its command, full output, every model request
body, ctxtra's log and Claude's debug log in `DIR/<test>/run<N>`. CI uploads that folder.

Agent binaries are found in this order:

1. `$CTXTRA_E2E_CLAUDE`, `$CTXTRA_E2E_CODEX`, `$CTXTRA_E2E_PI`, `$CTXTRA_E2E_OPENCODE`, `$CTXTRA_E2E_COPILOT`,
   `$CTXTRA_E2E_MUSE`
2. The pinned versions in `agents/`, installed with `agents/install.sh`. Muse Code isn't on npm:
   `agents/install-muse.mjs` downloads the version in `agents/muse.json` from Meta's release manifest and
   checks its sha256
3. `PATH`

An agent that isn't found is skipped; with `CTXTRA_E2E_REQUIRE=1` (CI) it fails.

In a Linux container, with network access limited to the container's loopback:

```bash
docker build -f e2e/Dockerfile -t ctxtra-e2e .    # from the repository root
docker run --rm --network none ctxtra-e2e
docker build -f e2e/Dockerfile --build-arg AGENTS=latest -t ctxtra-e2e-latest .   # latest agent releases
```

## What is covered

| Test | Checks |
|---|---|
| `TestStartup` | Bound files reach the model at session start, once each, in config order after the repo's `AGENTS.md`, in a worktree outside the project folder; a binding for another repo adds nothing; the hook ran; no checkout changed |
| `TestSubagent` | A subagent gets the files; the main agent still has them once after tool calls |
| `TestSkills` | The skill's name and description are listed, its body isn't, until the agent reads `SKILL.md` (or loads it with OpenCode's `skill` tool); a skill bound to another repo isn't listed |
| `TestCoexist` | Another tool's session-start hook keeps working next to ctxtra's and after `setup --remove` |
| `TestUntrustedHookAddsNothing` | Codex without hook trust: the session works and gets nothing from ctxtra |
| `TestUserMessage` | Claude and Muse get the one-line message for the user from the hook |
| `TestOutsideGit` | Outside git, only `global` and `dirs` bindings apply |
| `TestResume` | Copilot and Muse: a resumed session has the current version of the files, once |

A scenario that needs a capability (`driver.Cap`) runs only for agents that declare it, and is skipped
with the reason for the others. A declared capability that stops working fails the test.

Not covered here, and checked by hand before a release:

- What the interactive TUI shows (the session-start message on screen), `/clear`, `/compact`, and resume in
  Claude, Codex, Pi and OpenCode. These need a pseudo-terminal runner.
- Codex hook trust through `/hooks`. These tests run Codex with `--dangerously-bypass-hook-trust`, which
  checks delivery only.
- Cursor Agent CLI: its model protocol can't be mocked.
- Whether a real model follows the files and skills.

## Add an agent

1. Support it in ctxtra: `internal/agent/<name>`, and add it to the registry.
2. Add `driver/<name>.go`: a `Driver` that runs the agent non-interactively against the mock, with its
   config folder inside the fixture's home, and declares its capabilities. Add it to `driver.All`. If the
   agent speaks an API the mock doesn't, add it to `mock/`.
3. Pin it in `agents/package.json`, run `npm install --ignore-scripts` in `agents/` to update
   `package-lock.json`, and review its install scripts before allowing any in `agents/install.sh`. An
   agent that isn't on npm needs its own pinned download with a checksum, like `agents/install-muse.mjs`.
4. Run `go test -count=1 . -agent <name> -v`. Every scenario must pass or skip with a capability reason.

## Update the pinned agents

Dependabot opens a PR for new agent versions every week; the e2e workflow runs on it. Dependabot doesn't
know Muse Code. By hand:

```bash
cd e2e/agents
npm install --ignore-scripts @anthropic-ai/claude-code@latest @openai/codex@latest @earendil-works/pi-coding-agent@latest \
  @opencode/cli@latest @github/copilot@latest
node install-muse.mjs update   # pins the current Muse Code release in muse.json
./install.sh && cd .. && go test -count=1 ./...
```

The nightly `e2e-latest` workflow runs the tests against the latest versions and opens an issue when they
fail.
