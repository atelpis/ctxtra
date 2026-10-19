# Agent contract tests

These tests run real agents (Claude Code, Codex CLI, Pi, OpenCode, GitHub Copilot CLI, Muse Code, Antigravity CLI,
Hermes Agent, DeepSeek Harness) with ctxtra set up, and check what reaches the model. The model is a mock on
`127.0.0.1` ([mock/](mock)) that records every request and answers with
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
go test -count=1 . -keep -v                 # keep each run's home folder, and print where it is
```

`-agent` and `-keep` exist only in the tests of this folder (`.`), not in `./...`.

Always pass `-count=1`. The test cache doesn't know about ctxtra's sources (another module, built by
the tests) or the agent binaries, so a cached pass can hide a change in either.

With `CTXTRA_E2E_EVIDENCE=DIR`, a failed run saves its command, full output, every model request
body, ctxtra's log and Claude's debug log in `DIR/<test>/run<N>`. CI uploads that folder.

Agent binaries are found in this order:

1. `$CTXTRA_E2E_CLAUDE`, `$CTXTRA_E2E_CODEX`, `$CTXTRA_E2E_PI`, `$CTXTRA_E2E_OPENCODE`, `$CTXTRA_E2E_COPILOT`,
   `$CTXTRA_E2E_MUSE`, `$CTXTRA_E2E_ANTIGRAVITY`, `$CTXTRA_E2E_HERMES`, `$CTXTRA_E2E_DSH`
2. The pinned versions in `agents/`, installed with `agents/install.sh`. Muse Code isn't on npm:
   `agents/install-muse.mjs` downloads the version in `agents/muse.json` from Meta's release manifest and
   checks its sha256. Antigravity uses `agents/antigravity.lock` and a SHA-512 checked download to `agents/bin/agy`.
   Hermes is installed from the commit in `agents/hermes.ref` with the hashes of its `uv.lock`; it needs Python
   3.11 to 3.13 (the first one in `PATH`, or `$E2E_PYTHON`).
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
| `TestSubagent` | A subagent gets the files; the main agent still has them once after tool calls. In Antigravity a subagent is a session of its own, so the hook runs twice |
| `TestSkills` | The skill's name and description are listed, its body isn't, until the agent reads `SKILL.md` (or loads it with OpenCode's `skill` tool); a skill bound to another repo isn't listed |
| `TestCtxtraSkill` | The ctxtra skill that `ctxtra skill install` binds is listed like any other skill, and its body reaches the model only when the agent loads it |
| `TestCoexist` | Another tool's session-start hook keeps working next to ctxtra's and after `setup --remove` |
| `TestUntrustedHookAddsNothing` | Codex without hook trust: the session works and gets nothing from ctxtra |
| `TestUserMessage` | Claude and Muse get the summary message for the user from the hook |
| `TestOutsideGit` | Outside git, only `global` and `dirs` bindings apply |
| `TestResume` | Copilot and Muse: a resumed session has the current version of the files, once |
| `TestCapacity` | About 315K characters of bound files (a third for Copilot), Unicode and emoji included, reach the model at session start: every line once, in config order (parts that arrive out of order are put back by their labels). Agents that get the text from several hooks resolve the files once |
| `TestCapacitySubagent` | The same text reaches a subagent in full, and the main agent still has it once after tool calls |
| `TestOverCapacity` | Above the agent's capacity (see `capacity` in [capacity_test.go](capacity_test.go)), whole files are dropped from the end: the files before them arrive in full, the dropped one not at all; the log and the user message name it |
| `TestCopilotCompactionDropsText` | Documents a known Copilot limit: after Copilot compacts a long conversation, the hook's text is gone and the hook doesn't run again. Fails when Copilot changes that |
| `TestCodexTrustTUI` | Interactive Codex in tmux: one "Trust all and continue" in the hook review trusts ctxtra's hooks, which set a finite `additionalContextLimit`; the session gets the text in full; `setup` run again asks for no new trust. Needs tmux |

A scenario that needs a capability (`driver.Cap`) runs only for agents that declare it, and is skipped
with the reason for the others. A declared capability that stops working fails the test.

Not covered here, and checked by hand before a release:

- What the interactive TUI shows (the session-start message on screen), `/clear`, `/compact`, and resume in
  Claude, Codex, Pi and OpenCode. These need a pseudo-terminal runner.
- Codex hook trust through `/hooks`. The other tests run Codex with `--dangerously-bypass-hook-trust`;
  `TestCodexTrustTUI` covers the startup hook review only.
- Cursor (Agent CLI and desktop app): its model protocol can't be mocked. [cursorkit](cursorkit) checks it by
  hand with the real model: `go run ./cursorkit prepare -dir DIR -chars N [-delay D]` makes a test folder
  whose project hook binds a file with random codewords, `go run ./cursorkit cli -dir DIR` runs a CLI chat,
  and `go run ./cursorkit verify -dir DIR` checks a desktop chat opened in `DIR/workspace`: every codeword
  in the answer, read from Cursor's transcript, and no tool call. The codewords are samples: they show that
  the text arrived at those places, not every line between them or their order.
- The Antigravity App and IDE. They read the same hook file as the CLI.
- Hermes with `plugins.isolation: host`.
- DSH's web UI and TUI: the tests run its headless profile. DSH 0.2.0-rc.2's headless profile exits before a
  subagent's first request, so `TestSubagent` needs 0.2.1 or later.
- Devin CLI and Devin Local: they need a Devin login before a session starts (also with `-p`), and get
  models from Cognition's API server over an undocumented protocol, with no own API key or base URL. The
  CLI test `setup_devin` covers the hook, and that Claude's hook adds nothing when Devin runs it.
- Whether a real model follows the files and skills.

## Add an agent

1. Support it in ctxtra: `internal/agent/<name>`, and add it to the registry.
2. Add `driver/<name>.go`: a `Driver` that runs the agent non-interactively against the mock, with its
   config folder inside the fixture's home, and declares its capabilities. Add it to `driver.All`. If the
   agent speaks an API the mock doesn't, add it to `mock/`.
3. Pin it in `agents/package.json`, run `npm install --ignore-scripts` in `agents/` to update
   `package-lock.json`, and review its install scripts before allowing any in `agents/install.sh`. An
   agent that isn't on npm needs its own pinned download with a checksum, like `agents/install-muse.mjs`, or a
   pinned commit with a lock file, like Hermes in `agents/hermes.ref`.
4. Run `go test -count=1 . -agent <name> -v`. Every scenario must pass or skip with a capability reason.

## Update the pinned agents

Dependabot opens a PR for new agent versions every week; the e2e workflow runs on it. Dependabot doesn't
know Muse Code. By hand:

```bash
cd e2e/agents
npm install --ignore-scripts @anthropic-ai/claude-code@latest @openai/codex@latest @earendil-works/pi-coding-agent@latest \
  @opencode/cli@latest @github/copilot@latest @deepseek-ai/dsh@next
node install-muse.mjs update   # pins the current Muse Code release in muse.json
./pin-antigravity.sh          # pins the Antigravity CLI release
./install.sh && cd .. && go test -count=1 ./...
```

Dependabot doesn't update Hermes. To move its pin, put a newer release tag's commit in `agents/hermes.ref`
(the `^{}` line of `git ls-remote --tags https://github.com/NousResearch/hermes-agent.git`; the other line is
the tag object, not the commit) and run `./install.sh`.

The nightly `e2e-latest` workflow runs the tests against the latest versions and opens an issue when they
fail.
