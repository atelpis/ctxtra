# Proposal: upgrades without `ctxtra setup`

Status: final, 2026-10-18. Reviewed in three rounds; the round-3 fixes (P5 client limits, Hermes fixture
conversion, P6 conflicts, fixture paths, `setup.json` paths) were applied after the last round.

## Summary

After some upgrades of the ctxtra binary, users must run `ctxtra setup` again, or their sessions get less
text than this version can deliver. Before ctxtra is open-sourced, this proposal:

1. defines what `setup` writes as a contract between the agents and the binary, with a compatibility
   policy and kinds of contract change (P1);
2. freezes that contract once, with decisions on every item that could make it change later, including a
   way for the server to know how many slots a setup has (P2, P3);
3. makes a missing binary visible instead of silent (P6);
4. adds tests that fail when the contract changes by accident (P4), and when a newer binary serves an
   older setup differently from what that setup was promised (P5);
5. tells users from the CLI when a repair is needed, or when a new agent can be added (P7), from the first
   public release on.

It does not propose that the hook rewrites agent configs by itself (P8).

How to read this: [Background](#background) describes the current code; [Scenarios](#when-users-must-run-setup-after-an-upgrade)
lists the causes; [Research results](#research-results) has the measurements and facts the decisions rest
on; each P section has the design, the code changes and the tests; [Examples](#examples) shows what is on
disk and what the user sees; [Implementation plan](#implementation-plan) orders the work.

## Background

### Two parts of a setup

```
agent (Claude Code, Codex, OpenCode, ...)
   │  the agent's hook protocol: set by the vendor
   ▼
client: what ctxtra setup writes
   - hook entries in the agent's JSON config: a command line per event (and per slot)
   - plugin files for agents without command hooks: TypeScript (Pi, OpenCode), JavaScript (DSH), Python (Hermes)
   │  ctxtra's protocol: argv + JSON on stdin → JSON on stdout
   ▼
server: the ctxtra binary (`ctxtra hook AGENT EVENT [--slot N]`)
   resolves bindings, fits the text to the agent's limits, builds the answer
```

There is no long-running ctxtra process: the agent starts the binary for every hook call. (The OpenCode
plugin caches the answer per session inside OpenCode, `opencode.go`.) Upgrading the binary replaces the
server. The client stays on disk as the older version wrote it until the user runs `setup` again. The rule
this proposal follows: **a new version changes only the server, and the server keeps serving every client
that a released version wrote, with what that client was promised.**

### What setup writes today

Default paths; most agents also honor an environment variable for their folder (`CLAUDE_CONFIG_DIR`,
`CODEX_HOME`, `COPILOT_HOME`, `XDG_CONFIG_HOME`, ...).

| Agent | File | Client shape that is part of the contract |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | events `SessionStart` (matcher `startup\|clear\|compact`) and `SubagentStart`; 40 slot hooks per event; timeout 10 s |
| Codex | `~/.codex/hooks.json` | same events and matcher; one hook per event; `additionalContextLimit: 250000`; `statusMessage`; Codex trusts each hook by a hash of its entry |
| Muse | `~/.config/muse/settings.json` | 40 slot hooks per event; `schema_version: 1` |
| Cursor | `~/.cursor/hooks.json` | one hook per event; `version` |
| Copilot CLI | `~/.copilot/hooks/ctxtra.json` | one hook per event; `version`; the file is ctxtra's own |
| Antigravity | `~/.gemini/config/hooks.json` | one hook per event, under the key `ctxtra` |
| Devin | `~/.config/devin/config.json` | one hook per event |
| Pi | `~/.pi/agent/extensions/ctxtra.ts` | plugin: events `session_start`, `resources_discover`, `before_agent_start`; reads `message`, `skills`, `files` |
| OpenCode | `~/.config/opencode/plugins/ctxtra.ts` | plugin: events `setup`, `context`, `subagent`; reads `text`, `skills` |
| Hermes | `~/.hermes/plugins/ctxtra/__init__.py`, `plugin.yaml` | plugin: 125 `pre_llm_call` callbacks; sends `ctxtra_plugin: 2`; reads `chunks` (plugins before version 2: `context`); must be enabled in Hermes |
| DSH | `~/.dsh/ctxtra.mjs`, and a block in `~/.dsh/cordis.patch.yml` with the plugin's absolute path | plugin: events (the agent's `source`) and `subagent`; reads `text` |

Common to all of them:

- Every command hook uses `commandTemplate` (`internal/agent/command.go`), with the binary path in it.
  When the binary is missing, the command drains stdin and answers `{}`.
- The Node plugins (Pi, OpenCode, DSH) have the binary path, a 10 s timeout and a 64 MiB output buffer in
  their code, and treat an error, a timeout or a non-JSON answer as "nothing to add". The Hermes plugin
  (`hermes.go`, `_run`) reads stdout without a size bound and without checking the exit code.
- `ctxtra hook` parses its argv by position (`internal/cli/cli.go`): `--slot N` is recognized only as the
  third and fourth argument after `hook`. Any other argument count makes the hook run without a slot.

### How older setups are served today

- `agent.Upgrader` (`internal/agent/agent.go`): `Legacy(event, in)` tells whether the calling hook was written
  by an older version and what that hook can deliver in full. `hook.session` (`internal/hook/hook.go`) then
  fits the text to the old limit, dropping whole files, and adds a notice "run ctxtra setup" to the session
  summary and the log. The user sees the notice only in agents that show a session message; elsewhere it
  is in the log and in `doctor`. Implemented by:
  - Claude and Muse: the hook has no `--slot` (from before 40 slots): one part, 9,000 UTF-16 units or
    13,000 JSON bytes;
  - Codex: `hooks.json` has no `additionalContextLimit` equal to `250000` on ctxtra's handlers for the event
    (`hookjson.Config.Limited`): about 9,000 bytes;
  - Hermes: the plugin sends `ctxtra_plugin` below 2: one callback.
- Answers that two client versions both read: the Hermes answer has `chunks` for the current plugin and
  `context` for older ones.
- `hookjson.Config.Status` and `extfile.Status` (used by `ctxtra doctor`) report a missing binary, missing
  slots and a missing context limit, by reading the config files. Codex's `Status` also runs
  `codex --version`. Hermes's `Status` compares the whole plugin file with what this version would write,
  so any change of the generated code makes `doctor` ask for setup.
- `e2e/upgrade_test.go` (`TestUpgradeWithoutSetup`): runs real agents with configs that the previous version
  wrote (`e2e/testdata/upgrade/`, for Claude, Codex, Muse and Hermes) and checks the *reduced* text, no cut
  file, and the notice.
- `skill.Refresh` updates the installed ctxtra skill from the hook, when it is installed and writable.

## When users must run setup after an upgrade

| # | Cause | Today |
|---|---|---|
| S1 | The client shape changes: more slots, a new key value, new plugin code | Old hooks keep working with less text (`Legacy`) and a notice |
| S2 | The binary moves: a manual install in a versioned folder, a switch between `go install` and Homebrew, a version manager with versioned paths | The hook finds no binary and answers `{}`. The session gets nothing; nobody is told |
| S2b | Two binaries: a new one installed elsewhere, the old one still there | The hooks keep running the old version; nobody is told |
| S3 | A new agent: a ctxtra release supports a new agent, or the user installs a supported agent after setup | The agent has no hooks; only `ctxtra doctor` or the release notes show it |
| S3b | A setup that was there is gone or incomplete: a deleted hook file, a Hermes plugin that was disabled, a DSH patch row removed | Only `doctor` shows it |
| S4 | The agent changes its side: config location or format, trust records | Depends on the agent; looks to the user like "the upgrade broke it" |
| S5 | Codex: any change of a ctxtra entry in `hooks.json` | Codex skips the changed hook until the user trusts it in `/hooks` |
| S6 | A downgrade after a setup by a newer version | Works only if the older binary understands the newer argv |

S1 is the one ctxtra controls fully, and the main target. S2, S2b, S3 and S3b get notices. S4 can't be
prevented, only detected (`doctor`, the e2e tests against the latest agent versions). S5 makes every S1
change for Codex more expensive. S6 gets a tolerant argv parser.

## Goals and non-goals

Goals:

- After the first public release, a binary upgrade alone gives every session what its setup was promised,
  for every setup written by a release since then. A capacity change (P1) is the only exception, and it is
  announced.
- When a repair or a re-run is needed or useful, the user learns it from ctxtra, with the reason and the
  exact command.
- An accidental contract change fails CI.

Non-goals:

- Keeping configs written by pre-release versions working at full capacity.
- New capabilities (a new agent event, a new agent) without setup. They need it; the goal is that they are
  rare, optional and announced.
- Changing agent configs without the user running a command.
- Changing how ctxtra fits text to an agent's capacity (see [Risks](#risks) for the skill list).

## Research results

Measured or looked up on 2026-10-18.

### Cost of extra Claude slots

Benchmark: N concurrent `ctxtra hook claude SessionStart --slot K` processes with one session ID, an
isolated home folder, Apple Silicon, two runs each. Method: build the binary, write a config with one
global file, start N processes in the background with the same Claude `SessionStart` stdin (`session_id`,
`cwd`, `source: startup`), `wait`, and measure the wall time. Not reproduced by the reviewer.

| Slots | Text 5,000 chars (1 part) | Text 300,000 chars (35 parts) |
|---|---|---|
| 40 | 167–331 ms | 1,033–1,170 ms |
| 80 | 174–195 ms | 1,111–1,182 ms |
| 125 | 203–213 ms | 1,127–1,131 ms |

Empty slots cost almost nothing in ctxtra: a slot past the last part reads the snapshot and answers at once,
without waiting for the order chain (`hook/slot.go`). The time grows with the number of *parts* (each part
waits for the one before it, plus 20 ms), not with the number of slots. Not measured: the cost inside
Claude Code of starting more hook processes (one `/bin/sh` each).

Claude's 360,000 characters are 40 × 9,000: ctxtra's choice, not a Claude limit. The requirement is 100,000+
characters in full; 360,000 is 3.6 times that.

### Codex `additionalContextLimit`

From the Codex hooks documentation ([learn.chatgpt.com/codex/hooks](https://learn.chatgpt.com/codex/hooks)):

- Without the key, Codex shows the model about 2,500 tokens and stores the rest on disk.
- A positive integer is a different threshold; no maximum is documented.
- `0` passes the handler's complete additional context to the model. The docs advise `0` only when the hook
  caps its output strictly itself.
- The `SessionStart` matcher values are `startup`, `resume`, `clear`, `compact` and `fork`.

Why ctxtra keeps a finite value (the first draft proposed `0`; review round 1 showed why not):

- ctxtra doesn't cap its output strictly: `resolve.Result.Fit` never drops the skill list, so the text can
  stay over the capacity (`internal/resolve/resolve.go`, comment of `Fit`).
- The choice of a finite limit in #21 was deliberate: `e2e/tui_test.go` (`checkCodexLimits`) fails on `0`.
- `Delivery()` computes the capacity from the same constant (`contextLimit * 4`), so the constant can't
  simply become `0`.
- 1,000,000 bytes are 10 times the requirement, so a need to raise it soon is unlikely. Raising it later is a
  capacity change (P1), with a trust prompt.

### What plugin hosts let a plugin show the user

| Host | User message from a plugin | Source |
|---|---|---|
| Pi | `ctx.ui.notify(text, level)` when `ctx.hasUI` | used by ctxtra's extension today (`pi.go`) |
| OpenCode | Not verified for ctxtra's plugin. The toast API in OpenCode's docs belongs to its CLI-plugin context ([docs](https://dev.opencode.ai/v2/docs/build/plugins/cli)); ctxtra's plugin is a server plugin. The 2.0.29 binary has a `tui.toast.show` event | docs, `strings` of the binary |
| Hermes | None known (comment in `hermes.go`) | `internal/agent/hermes` |
| DSH | Not checked | — |

### Events and matchers

Hook events that the agents have and ctxtra doesn't use: Codex lists 12 (`SessionEnd`, `PreToolUse`,
`PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, `Stop`,
`Interrupt`, ...); Claude Code has a similar set. None is needed by a current feature. A new event is an
additive change (P1): old setups keep working without the feature. So events don't need to be decided
before the freeze, unless a feature is already planned.

Claude's and Codex's matchers leave out `resume` (and Codex's new `fork`). What a resumed or forked session
gets in these two agents is not covered by tests: `TestResume` needs the `Resume` capability, which the
Claude and Codex drivers don't declare (`e2e/driver/claude.go`, `e2e/driver/codex.go`).

## Proposal

### P1. Hook contract and compatibility policy

Add a section "Setup compatibility" to `docs/spec.md`, after "Capacity":

1. **The contract** is everything that `ctxtra setup` writes: hook entries (events, matchers, command lines,
   slot count, timeouts, other keys such as `additionalContextLimit`), plugin files (events they send,
   input fields, answer fields they read, constants), and the argv of `ctxtra hook`.
2. **Version 1** of the contract is what the first public release writes. Each distinct client gets an
   immutable fixture (P5); no separate contract number is stored.
3. **Kinds of change:**

   | Kind | Example | Old setups | Allowed |
   |---|---|---|---|
   | Server-only | a new answer field; a new notice; a smaller part size for an agent with one hook | unchanged: what they were promised | always |
   | Additive | a new event for a new feature; a new plugin field handler | unchanged, without the new feature | yes, with a release note and a `doctor` line |
   | Capacity | more slots; a higher Codex limit; a plugin version that carries more text | keep what they were promised, through `Legacy`, with a notice | rarely, with the checklist below |
   | Breaking | an old setup gets less than it was promised, or fails | worse | never |

   A change is server-only only if every historical fixture keeps its promised delivery (P5). For example,
   a smaller part size for Claude is a breaking change for 40-slot setups unless the number of parts still
   fits their slots (`Delivery.Fits` checks both size and parts).
4. **Checklist for an additive or capacity change**, in the same pull request:
   - for a capacity change: a `Legacy` case that recognizes the older client from what it sends or from
     the value written in its config (not from "differs from the current value"), with a test;
   - updated golden files (P4) and a new client fixture with its expected delivery (P5);
   - a release-notes line: "Run `ctxtra setup --agent <agents>` after this upgrade: <reason>" (plus "Codex
     will ask you to trust the hooks again" when Codex is affected);
   - for an additive change, a line in `doctor` that names the feature an old setup doesn't have.
5. **Answers only grow.** Fields are added, never renamed or given a new meaning. Clients ignore fields they
   don't know. A field that a released client reads is always sent while that client is supported.
6. **Argv and event names** that a released client sends are accepted forever. From version 1 on, `ctxtra
   hook` ignores arguments it doesn't know (P2.5), so a downgraded binary still runs a newer client.

### P2. Freeze the command hooks

Decisions for each item. Changing an item later is an additive or capacity change (P1.3), and for Codex
also a trust prompt for every user. Only item 1 changes the Codex entries.

1. **Command template: add a visible fallback (P6).** Ships in the last release before the public one, to
   cost one Codex trust prompt, for the author and early testers only.
2. **Codex: keep `additionalContextLimit: 250000`.** Reasons in [research](#codex-additionalcontextlimit).
   No code change. One change for robustness, needed only when the value ever changes: `hookjson` should
   return the limit as written, typed (absent, an integer, invalid), so a later `Legacy` can compute the
   capacity of an old value instead of treating every other value as the 2,500-token preview. Not part of
   the freeze; listed in the P1.4 checklist.
3. **Slots: keep 40 for Claude and Muse, 125 for Hermes, and write the count into the command.**

   Today a server can't tell a 40-slot setup from a setup with more slots: both run `--slot 0`, `--slot 1`,
   and so on. A later version with 80 slots would split the text into up to 80 parts, and an old setup would
   run only 40 of them: the rest would be lost without a word. So, from version 1:

   - `agent.SlotCommand` writes `hook claude SessionStart --slot 3 --of 40`.
   - The server reads `--of N` and uses `min(N, Delivery().Slots)` slots for `Fits` and `Chunks`. Without
     `--of` (a setup written before version 1, with slots) it uses 40, the only count ever written.
   - Validation: `N > 0` (`Delivery.Slots == 0` means "any number" in `agent.Delivery`, so `--of 0` must not
     reach it); `--of` only together with `--slot`; `0 <= K < N`. Anything else is an invalid argv (P2.5).
     A valid slot that is past the server's own slot count (a downgrade) answers empty.
   - The effective delivery is chosen once per invocation and carried through: `runSlot` fits and chunks
     with it, instead of calling `a.Delivery()` again (`hook/slot.go`, `resolveNow`). The slot count is part
     of the snapshot key (`slotKey`), so a 40-slot and a 64-slot client of the same session never share a
     snapshot. Test: the same session input with `--of 40` and `--of 64` within the join window gets two
     snapshots, each fitted to its count.
   - `commandRes` and `CommandSlot` in `command.go` match the new form; `oldTemplates`-style matching keeps
     the form without `--of`, so setup updates such entries in place.
   - Hermes sends the count it registered in its input (`"ctxtra_slots": 125`), for the same reason;
     without it, 125. The same validation applies: a count that isn't a positive integer gets the empty
     answer and a log entry.
   - Claude, Muse and Hermes configs have no trust records, so this costs no prompt. It ships with item 1.

   The count stays at 40: the measured cost of more slots in ctxtra is small, but 360,000 characters are well
   above the requirement, the cost inside Claude Code is not measured, and a larger context costs the user
   tokens in every session. With `--of`, raising the count later is a clean capacity change.
4. **Events and matchers: keep.** A new event is additive. Open for the author: is a feature planned that
   needs one (per prompt, compaction)? Open for verification: what resumed and forked Claude and Codex
   sessions get ([research](#events-and-matchers)); add `fork` to Codex's matcher only if a forked thread
   loses the parent's context.
5. **Tolerant argv parsing.** `internal/cli/cli.go`, case `"hook"`:
   - after `AGENT EVENT`, read `--slot N` and `--of N` wherever they are, and ignore other arguments;
   - a value that isn't a non-negative integer, or a second `--slot` or `--of`, makes the hook answer the
     agent's empty answer and log an error. Running without a slot instead would put the whole text in
     every slot of a Claude session.
   - Tests in `cli_test.go`: `--slot 3 --of 40 --future x` runs slot 3 of 40; `--future --slot 3` runs slot
     3; `--slot x` and `--slot 1 --slot 2` answer empty and log.
   This is only for S6 (a newer client, an older binary), so it must be in the first public release.
6. **Timeouts: keep 10 s** (`hookjson.Timeout`, the Node plugins' `timeout: 10000`). The slot coordination
   waits add up to 8.1 s, not counting the time to resolve (`hook/slot.go`).

### P3. Plugin protocol

Plugins are code, so they are the most likely to need a change. Version 1 of the plugin protocol, for Pi,
OpenCode, DSH and Hermes:

1. **Input: the plugin sends its version** as `"ctxtra_plugin": N` in the stdin JSON, next to `cwd`.
   - Pi, OpenCode, DSH: start at 1. A plugin without the field is a pre-release plugin; it gets what it
     gets today (they have no `Legacy` case, and none is needed).
   - Hermes: keeps its own sequence. Version 2 (today's) stays a full-capacity client; version 3 adds
     `ctxtra_slots` (P2.3). `hermes.Legacy` must then compare with 2, the first version with several
     callbacks, not with `pluginVersion` as today (`in.Plugin < pluginVersion`): otherwise raising
     `pluginVersion` to 3 would give every version-2 plugin the one-callback capacity.
   - `internal/hook/hook.go`: the comment of `payload.Plugin` says "Hermes"; make it general.
2. **The file says its version**, so the CLI can check it without running it (P7). `extfile.Content` writes
   a third header line, `// protocol: N` (`# protocol: N` in Python), after the mark and the binary lines.
   `extfile` gets `func Protocol(path string) (int, bool)`. A file without the line is pre-release; for
   Hermes, `Protocol` falls back to the `"ctxtra_plugin": N` literal in the code, so today's version-2
   plugins are recognized.
3. **A version is a compatibility fact, not a freshness check.** A lower version alone is not a reason to
   ask for setup. Setup is asked for only when the server, for that version, delivers less than a fresh
   setup (a `Legacy` case) or the version lacks a feature that the user has (P7). Replace Hermes's
   whole-file comparison in `Status` with this rule.
4. **Output: an object that only grows** (P1.5). The plugins already read named fields and ignore others.
   Real-agent e2e runs with historical plugins protect this (P5).
5. **Wire the features that the hosts allow, now:**
   - Pi: on `session_start`, when the binary can't be run (the `execFile` error code is `ENOENT` or
     `EACCES`), `ctx.ui.notify(<the P6 message>, "warning")`. Today the extension adds nothing and says
     nothing.
   - OpenCode: only if a toast API is found for server plugins: the same notice, and an optional `message`
     field in the `context` answer for later use.
   - Hermes: check the exit code of `ctxtra` and bound the output it reads (64 MiB, like the Node plugins).
     No user message API.
   - DSH: nothing.
6. **Constants: keep.** The 10 s timeout and the 64 MiB output buffer: 1,000,000 characters are at most
   about 6 MB of JSON (`\u00XX` escapes for control characters, 4 bytes for other non-ASCII), plus the skill
   bodies in OpenCode's answer.
7. **Version negotiation for later changes:** the binary reads `ctxtra_plugin` and answers each version in
   the format that version reads, or with fields for both (the Hermes `chunks`/`context` pattern).

Alternative considered: a generic list of actions (`{"actions": [{"type": "context", ...}, {"type":
"notify", ...}]}`) that every plugin interprets. It is more uniform, but the hosts differ (Pi adds context
files, OpenCode pushes system text, DSH injects a user message, Hermes uses callbacks), so the actions
would still be per agent. Additive per-agent fields give the same compatibility with less change.

### P4. Golden tests for the contract

**Test isolation first.** `testenv.Home` clears the agents' folder variables, but not `COPILOT_HOME`
(`internal/testenv/testenv.go`, `internal/agent/copilot/copilot.go`). A test that runs `setup` for every agent
could write into a developer's real Copilot folder. Add `COPILOT_HOME` and every other variable that an
agent's `dir()` reads, and add a test that compares that list with the agents (for example, a helper per
agent that names its variables).

**The test,** `internal/setup/contract_test.go`:

```go
// TestContract: what setup writes is the contract with the agents
// (docs/spec.md, Setup compatibility). A difference from testdata/contract
// is a change of the contract: see the checklist there before -update.
func TestContract(t *testing.T) {
	for _, a := range registry.All() {
		t.Run(a.Name(), func(t *testing.T) {
			h := testenv.Home(t)
			o := Options{Agents: []string{a.Name()}}
			o.Binary = filepath.Join(h, "bin", "ctxtra") // written as $HOME/bin/ctxtra
			// PATH is an empty folder, so Detect finds nothing else
			// run; fail if any write lands outside h
			// walk h (not r.Changes), except ~/.ctxtra: every file, with h
			// replaced by @@HOME@@, equals testdata/contract/<agent>/<path>,
			// and no file is missing or extra; -update rewrites the folder
		})
	}
}
```

- Runs in the normal `go test ./...` (fast, no agents).
- Compares the whole home folder, so a file that setup stops writing fails the test too.
- Normalizes absolute paths under the fixture home to the sentinel `@@HOME@@`. Shell `$HOME` and
  `join(homedir(), ...)`, which most files hold, stay as they are; DSH's patch holds the plugin's absolute
  path (`dsh.patchBlock`), which becomes `@@HOME@@/.dsh/ctxtra.mjs`. A plain text replacement is enough
  only for paths that need no escaping in JSON, YAML, TypeScript or Python. `ValidBinaryPath` still allows
  tabs and carriage returns, so the exporter and the loader reject a home folder or binary path with any
  character outside `[A-Za-z0-9_./-]` (`_` because macOS temporary folders have it). The loader also checks that each JSON and
  YAML file still parses.
- `PATH` is an empty folder during the test (as `setup_test.go` does), so `Detect` can't find agents
  installed on the machine. `~/.ctxtra` (backups, `setup.json`) is not part of the contract and is left out;
  P7 tests `setup.json` with fixed detection results.
- A harmless change (a comment in a plugin) is updated with `-update`; a real one follows P1.4. The test's
  doc comment points there.
- Hermes's setup reads whether the plugin is enabled, and Codex's adds a note: both change only notes, not
  files. Check that while writing the test.

**Server side, cheap:** `internal/cli/contract_test.go`. For each client fixture (P5; the fixture schema,
the first fixtures and the loader come in the same pull request) and each command hook in it, run the
command's argv through `cli.Run` (so the argv parser of P2.5 is covered), with configs below, near and above
the fixture's capacity, and compare the delivered text with what the fixture's capabilities promise (P5).
The test fails when it ran no fixture. Plugins are covered by P5's e2e runs, which execute the historical plugin
code in the real agent.

### P5. Upgrade tests across releases

**Fixtures:** `testdata/clients/<id>/` (shared by the unit test of P4 and the e2e tests; the e2e module
reads it by a relative path) holds one distinct client per agent, normalized as in P4, plus `client.json`
with the limits that the client itself sets. These are facts about the client, written once and never
changed. A limit that is only the server's policy (Pi's 1,000,000 characters, for example: the extension
itself only has a 64 MiB buffer) is not a client limit and is left out:

```json
{"agent": "claude", "slots": 40, "events": ["SessionStart", "SubagentStart"]}
{"agent": "claude", "slots": 1, "events": ["SessionStart", "SubagentStart"]}
{"agent": "codex", "limit": {"value": 1000000, "unit": "bytes"}, "events": ["SessionStart", "SubagentStart"]}
{"agent": "pi", "protocol": 0, "events": ["session_start", "resources_discover", "before_agent_start"]}
```

The expectation is computed against the server under test, not stored. The client can carry what the
server's policy for that client allows within the client's limits: for slotted clients, `slots` parts of
the server's part size; for Codex, `limit`; for clients without limits, the server's capacity. The server's
`Legacy` gives exactly that. The test expects everything up to that amount, whole files dropped above it,
and the "run ctxtra setup" notice only when a fresh setup would carry more. So a later server-only increase
reaches unchanged clients that can carry it, and a capacity increase turns a constrained client into a
`Legacy` client, without editing either fixture.

Units are the server's (`chunk.Unit`): Unicode code points, UTF-16 units (Claude, Muse), bytes, and for
Muse's parts, bytes of the JSON answer (`ChunkJSON`). Test texts are sized per agent in its unit.

A new fixture is added only when P4's golden files change; `testdata/clients/releases.json` maps each release
to the fixture id per agent. Today's `e2e/testdata/upgrade/` files become the first fixtures (`pre-v1`).
Their `CTXTRA_BIN` placeholder is converted once, by syntax: to `$HOME/bin/ctxtra` inside shell commands
(the JSON hook configs), and to `@@HOME@@/bin/ctxtra` in the Hermes plugin, which passes the path to
`subprocess.run` without a shell, so `$HOME` would not be expanded there. A loader test runs the converted
Hermes plugin's `_run` against the binary. The old placeholder is no longer supported.

**Loader:** writes a fixture into the session's home folder, replacing `@@HOME@@`, checks that each JSON and
YAML file still parses, and links the tested binary at `$HOME/bin/ctxtra` (today the e2e binary lives in a
separate temporary folder, `harness_test.go`), so the fixture's commands run it unchanged.

**Tests:**

- `TestUpgradeFromRelease` (replaces `TestUpgradeWithoutSetup`): for every fixture and every agent with a
  driver, run sessions with text below, near and above the fixture's capacity, sized per agent and unit (as
  `capacity_test.go` does), and check the computed expectation.
- `TestCodexUpgradeKeepsTrust` (in `tui_test.go`, which drives the real TUI without
  `--dangerously-bypass-hook-trust`): set up, trust the hooks, replace the binary with a newer build, start
  again: no "Hooks need review" screen, full text.
- At each release: the release checklist adds the release to `releases.json`, and a fixture only if P4's
  files changed.
- The e2e workflows run on `workflow_dispatch` while the repository is private. Run these tests before each
  release; after the repository is public, in the scheduled run.

### P6. Binary path

Today `setup.Binary` writes the binary as found in PATH, without resolving symlinks, so a Homebrew or
`go install` upgrade keeps the same path. When the binary is gone, the template answers `{}`.

Options considered:

- **A. Keep the template**; recommend Homebrew or `go install` in the README; report problems in `doctor`
  and the CLI (P7).
- **B. A stable link** `~/.ctxtra/bin/ctxtra` that hooks run, which any `ctxtra` command repoints to itself
  when its target is missing. Repairs S2 without setup, but a test or development build can repoint it,
  and it is one more thing to explain.
- **C. Fall back to `ctxtra` in PATH.** Hooks often run with a minimal PATH (GUI apps on macOS), and another
  `ctxtra` earlier in PATH would run with the agent's rights. Rejected.
- **D. A visible failure:** the fallback branch prints an answer with a fixed user message where the agent's
  format has one.

**Recommendation: A + D**, and B not now. D changes the template, so it ships with P2.3. Design of D:

- The message is fixed text per agent, without the path: `ctxtra: the ctxtra binary that this hook runs is
  missing; reinstall ctxtra, or run ctxtra setup --agent claude with the new binary`. For Codex it adds
  `(Codex will then ask you to trust the hooks again)`. Reason for no path: `$HOME` is expanded when the hook
  runs, and `ValidBinaryPath` allows tabs and carriage returns, so a path in the JSON could make it invalid.
  The CLI notice (P7) names the path.
- `internal/agent/command.go`:

  ```go
  const commandTemplate = `/bin/sh -c 'f="%s"; if [ -x "$f" ]; then exec "$f" hook %s %s; else /bin/cat >/dev/null 2>&1; echo %s; fi'`
  ```

  The last `%s` is a fixed shell word per agent and event, from a short list (below). The whole command is
  inside single quotes, so the word can't use them; it is a double-quoted string with `\"` for JSON quotes and
  no `$`, `` ` `` or `\` other than those: `"{\"systemMessage\":\"ctxtra: ...\"}"`, or `"{}"`.
- An optional agent interface:

  ```go
  // Missing is an agent whose hook answer can show the user a message.
  // MissingAnswer is the JSON the hook command prints when the binary is
  // gone, with msg for the user; "" for {}.
  type Missing interface {
  	MissingAnswer(event, msg string) string
  }
  ```

  Claude, Codex and Devin share `systemMessage` at `SessionStart` (`agent.ContextAnswer`); Cursor, Copilot,
  Antigravity and Muse are checked one by one and answer `{}` when they have no such field. `SubagentStart`
  gets `{}`. Slotted agents: only slot 0 gets the message.
- **Recognition stays exact.** `CommandBinary` decides which entries are ctxtra's, and setup replaces or
  removes those (`hookjson.isOurs`). So `commandRes` matches the exact released templates with only the
  fallback words that ctxtra writes (the list above), not any text.
- **A changed ctxtra entry is a conflict, not a reason to add another.** Today setup keeps entries it doesn't
  recognize and adds its own (`hookjson.Apply`), so a ctxtra hook whose fallback the user edited would run
  next to a new one, and the session would get the text twice. Add a looser match, `looksOurs`: a command
  that contains ` hook <agent> ` and the name `ctxtra`. When an event has such an entry and no exact one,
  setup leaves the file unchanged for that agent and reports: `<path>: a ctxtra hook was changed by hand;
  left alone. Restore or delete it, then run ctxtra setup --agent <agent>`. `setup --remove` leaves such
  entries and reports them the same way. `Check` (P7) reports them too. Tests: an edited fallback is kept by
  setup and by `--remove`, no second ctxtra hook is added, and the message names the file.
- `oldTemplates` gets today's template, so setup updates entries in place (Codex keys trust by position).
- Agents that run another agent's hooks (Cursor desktop and Devin run Claude's): when ctxtra is missing,
  they may show Claude's message too. Acceptable: the message is true for them as well.
- Tests: `command_test.go`: `CommandBinary` and `CommandSlot` on old and new templates; a test that runs each
  generated command with a missing binary and checks that the output is valid JSON with the message. e2e:
  one agent with a missing binary shows the message (`message_test.go` has the pattern).

### P7. Tell users when setup is needed

Ships **before the first public release**: users of that release must already have `setup.json`, or they
would only get new-agent notices after a re-run.

**Where:** `internal/cli/cli.go`, `Run`: after a command other than `hook`, `help`, `-h`, `--help`,
`version` and `setup`, call `setupNotices(stdio.Err)`. It prints one line per problem to stderr, through the
same `term.Writer` as other output, and never changes the exit code. A failure of the check itself (an
unreadable file) is one short line, never an error.

**What it checks, per agent:**

| Check | Notice | Source |
|---|---|---|
| Set up, the binary in the config doesn't exist (S2) | `claude: the hooks run ~/Downloads/ctxtra-0.3/ctxtra, which doesn't exist; run ctxtra setup --agent claude` | `CommandBinary`, `extfile` binary line |
| Set up, the binary in the config is not this binary (S2b) | `claude: the hooks run /usr/local/bin/ctxtra, not this ctxtra (/opt/homebrew/bin/ctxtra); to switch, run ctxtra setup --agent claude` | compare `paths.Real` of both; skipped when this binary is in a temporary folder (`go run`) |
| Set up with less capacity (a `Legacy` case) (S1) | `codex: set up by an older ctxtra, with Codex's limit of 9,000 bytes; run ctxtra setup --agent codex (Codex will then ask you to trust the hooks again)` | config form of `Legacy` |
| Set up before, now missing or incomplete (S3b) | `hermes: the ctxtra plugin is installed but not enabled; run hermes plugins enable ctxtra` / `dsh: the patch row that loads ctxtra's plugin is missing; run ctxtra setup --agent dsh` | agent-specific, below |
| Detected since the last setup (S3) | `cursor: found since the last ctxtra setup; to add ctxtra to it, run ctxtra setup --agent cursor` | `setup.json` |

Every notice names the agent in its command (`--agent`), so a repair doesn't set up agents the user left out.
Every notice whose repair changes a Codex entry says that Codex will ask to trust the hooks again.

**New method.** `Agent.Status` can't be reused, because Codex's runs `codex --version` (an exec on every CLI
command). Add to the `Agent` interface:

```go
// Check returns what the user should do about this agent's setup, from
// its files only. known is whether setup.json lists the agent's current
// config path as set up, so a setup that is gone is told from one that
// never was. bin is the
// running binary, "" when it can't be compared.
Check(bin string, known bool) []string
```

- Shared helpers: `hookjson.Config.Check` (missing or other binary, missing or wrong slots, a context limit
  that gives less than the capacity, no entries although `known`) and `extfile.CheckSetup(path, bin)`
  (missing or other binary, a protocol version with a `Legacy` case, no file although `known`). Rename today's
  `extfile.Check` (it checks that a file is ctxtra's) to `extfile.Owned`.
- Agent-specific parts: Hermes: the manifest is there, and the plugin is enabled (a disabled plugin is told
  as "not enabled", not as "not set up"); DSH: the plugin file and the patch row are both there.
- All reads are bounded and of regular files only (`fsutil.ReadBounded`), so a FIFO or a huge file can't
  block or exhaust a CLI command. Each config file is parsed once per run.
- `Status` reuses `Check` for its details, so `doctor` and the CLI say the same thing.

**State:** `~/.ctxtra/setup.json`, written by `setup`:

```json
{"installed": {"claude": ["/Users/sam/.claude/settings.json"], "codex": ["/Users/sam/.codex/hooks.json"]},
 "detected": ["claude", "codex", "pi"]}
```

- `installed`: per agent, the config paths (`Agent.ConfigPath()`, which follows `CODEX_HOME` and the like)
  where setup succeeded, in this run or earlier. Stored as absolute paths, compared after `filepath.Clean`. An agent whose setup failed is not added. `setup --remove`
  removes the path after the removal succeeded. `known` (for `Check`) is true only when the agent's current
  config path is listed, so a Codex profile in another `CODEX_HOME` that was never set up is not reported as
  "gone"; it is reported, at most, as "not set up here" by `doctor`.
- `detected`: every agent that `Detect()` found at a setup, also those left out with `--agent`. Agents found
  later and in neither `installed` nor `detected` are "new".
- One lock, `~/.ctxtra/setup.lock`, is held for the whole `setup` run: the agent config changes and the
  `setup.json` update. Two setups (or a setup and a removal) at once run one after the other, so the
  configs and `setup.json` can't disagree, and two runs can't edit the same agent config at once. The file
  is written atomically (as `internal/state` does). `--dry-run` takes no lock and doesn't write it.
- When the configs changed but `setup.json` can't be written, setup reports the error and exits 1; the
  config changes stay (they are correct), and the next successful setup writes the file.
- A missing file: no new-agent notices, and `known` is false for every installation. A malformed file:
  one notice ("~/.ctxtra/setup.json is unreadable: notices about new or removed setups are off until the next
  ctxtra setup"), then as if missing. The notice doesn't suggest a setup of every agent just to repair this
  file.
- The file is advisory. It never authorizes a change of an agent config; only `setup` changes them.

**Tests** (`internal/cli`): each notice with a fixture config; no notice after a fresh setup; a disabled
Hermes plugin and a missing DSH row; `setup.json` after a partial failure and after `--remove`; a malformed
`setup.json`; a FIFO in place of a config file doesn't block; no output for `hook` and `version`; exit codes
unchanged.

### P8. Repair from the hook: not now

The hook could run the setup of its own agent when `Legacy` matches. Reasons not to:

- Concurrency: up to 40 or 125 slot processes start at once, so it needs a file lock and one slot only.
- The change applies only from the next session.
- A hook that edits agent configs without being asked is surprising, and an agent sandbox may forbid the
  write anyway. Setup also takes care to keep other tools' entries, mixed groups and symlinks, with backups
  (`hookjson.Apply`, `fsutil.WriteAtomic`); that is easier to trust when the user runs it.
- Codex would skip the rewritten hook until the user trusts it again: worse than the reduced text. Hermes
  needs `hermes plugins enable` after a new plugin.

Revisit only if P1–P7 are in place and users still report the problem. If it is done: only ctxtra's own
entries, never Codex, always a notice.

### P9. Release and distribution

- Release notes have the fixed line of P1.4 when a change needs or allows a re-run, and only then.
- Homebrew runs post-install steps in a sandbox that can't write to the home folder
  ([Formula Cookbook](https://docs.brew.sh/Formula-Cookbook#running-commands-after-installation)), so it can't
  run setup. The formula's `caveats` say "run `ctxtra setup` once after installing"; P7 covers upgrades.
- Pre-release `Legacy` cases (Claude and Muse without `--slot`, Codex without the key, Hermes below 2):
  recommendation: keep them. They are small and tested, and early testers have such setups. Delete them in a
  later release, with a release note.

## Examples

### E1. Capacity change, Claude Code (S1): what happened with #21

Before the upgrade, `~/.claude/settings.json` has one ctxtra hook per event:

```json
"SessionStart": [{"matcher": "startup|clear|compact", "hooks": [{"type": "command", "timeout": 10,
  "command": "/bin/sh -c 'f=\"$HOME/go/bin/ctxtra\"; if [ -x \"$f\" ]; then exec \"$f\" hook claude SessionStart; else ...'"}]}]
```

The user upgrades with `go install` and starts Claude. The hook runs without `--slot`; `claude.Legacy`
matches; the session gets the files that fit 9,000 characters, and the start message says:

```
✓ ctxtra added 2 instructions; dropped big.md (over Claude Code's limit of 9,000 characters) (run ctxtra doctor)
Instructions: me.md, go.md
ctxtra: the hook is from an older version of ctxtra, with Claude Code's limit of 9,000 characters; run ctxtra setup
```

After `ctxtra setup`, the file has 40 entries per event, and the session gets everything.

**With the proposal:** version-1 entries are `... hook claude SessionStart --slot 0 --of 40` to `--slot 39
--of 40`. If a later version moves to 64 slots, a version-1 setup still gets 40 parts (`--of 40`), with a
notice, and nothing is lost silently. P4 fails until the author follows the checklist; the release note
says `run ctxtra setup --agent claude`.

### E2. A new answer field, OpenCode (server-only)

A later version wants OpenCode to show "✓ ctxtra added 2 instructions". This is server-only only if the
freeze found a toast API for server plugins and the version-1 plugin already reads `message` (P3.5). If the
API is found later, reading `message` is a plugin change: an additive change with an optional setup. With
the handler in place, the binary adds `message` to the `context` answer:

```json
{"text": "...", "skills": [...], "message": "✓ ctxtra added 2 instructions"}
```

A plugin that reads `message` shows it. An older plugin ignores the field and works as before. Nobody runs
setup.

### E3. Codex capacity (S1, S5)

`hooks.json` keeps `"additionalContextLimit": 250000`, and Codex gets 1,000,000 bytes. If a later version
needs more, it writes a higher value: a capacity change. Old setups keep 1,000,000 bytes (their `Legacy`
case reads the written `250000` and computes that capacity), the CLI says `codex: set up by an older ctxtra,
with Codex's limit of 1,000,000 bytes; run ctxtra setup --agent codex (Codex will then ask you to trust the
hooks again)`, and the user decides.

### E4. A moved binary (S2)

The user downloaded `~/Downloads/ctxtra-0.3/ctxtra`, ran setup, then deleted the folder and installed 0.4
with Homebrew.

Today: every hook prints `{}`. Sessions get nothing, and nothing says why.

With the proposal:

- Claude, Codex, Devin (P6, slot 0 only): `ctxtra: the ctxtra binary that this hook runs is missing;
  reinstall ctxtra, or run ctxtra setup --agent claude with the new binary` (Codex: with the trust note).
- Pi (P3.5): the same text as a warning notification.
- OpenCode, Hermes, DSH, and agents without a user message field: still nothing in the session.
- Any `ctxtra` command with the new binary (P7):
  `claude: the hooks run ~/Downloads/ctxtra-0.3/ctxtra, which doesn't exist; run ctxtra setup --agent claude`.
  For Codex, the line adds the trust note.

### E5. Two binaries (S2b)

The user installed 0.3 with `go install` (`~/go/bin/ctxtra`) and later 0.4 with Homebrew; PATH finds the
Homebrew one first. Today the hooks keep running 0.3, silently. With P7, `ctxtra sync` (Homebrew's 0.4)
prints: `claude: the hooks run ~/go/bin/ctxtra, not this ctxtra (/opt/homebrew/bin/ctxtra); to switch, run
ctxtra setup --agent claude`.

### E6. A new agent (S3)

The user ran setup for Claude and Codex, then installed Cursor. `~/.ctxtra/setup.json` has `"detected":
["claude", "codex"]`. The next `ctxtra files` prints: `cursor: found since the last ctxtra setup; to add
ctxtra to it, run ctxtra setup --agent cursor`. After that setup, `setup.json` has Cursor in `installed` and `detected`, and
the notice stops. If the user had run `setup --agent claude` while Cursor was already installed, Cursor would
be in `detected` only, and no notice would come.

The same happens when a ctxtra release adds support for an agent the user already has.

### E7. A setup that is gone (S3b)

The user disabled the Hermes plugin in Hermes. `setup.json` has the plugin's path under `installed.hermes`. The next CLI command
prints: `hermes: the ctxtra plugin is installed but not enabled; run hermes plugins enable ctxtra`.

### E8. An additive change (a new event)

A later version adds a feature on Claude's `UserPromptSubmit`. Old setups keep working without the feature.
The release notes say "run `ctxtra setup --agent claude` to enable <feature>"; `doctor` says `claude: <feature>
is not set up (run ctxtra setup --agent claude)`. No CLI notice on every command: nothing is broken.

### E9. A downgrade (S6)

A setup by a later version has a hook `... hook claude SessionStart --slot 3 --of 40 --part-size 12000` (a
hypothetical new flag). The user goes back to a version-1 binary. With P2.5 it ignores `--part-size` and
runs slot 3 of 40 as it knows it. A pre-release binary would see an unknown argument count and run without a
slot; downgrades below version 1 are not supported.

## Implementation plan

Pull requests in order. Each one passes `go test ./...`, `go vet ./...` and the repository's other checks; e2e
where noted.

1. **Freeze** (the last release before the public one; one Codex trust prompt, for the author and early
   testers):
   - P2.3: `--of N` in `SlotCommand`, `commandRes`, `CommandSlot`, the server's slot count; Hermes plugin
     version 3 with `ctxtra_slots`.
   - P6 D: template, exact fallback words, `oldTemplates`, `agent.Missing` for Claude, Codex, Devin (others
     `{}`), recognition tests.
   - P2.5: tolerant argv.
   - P3.1, P3.2, P3.3, P3.5: `ctxtra_plugin` and the protocol line in Pi, OpenCode, DSH; `extfile.Protocol`;
     Hermes `Status` by protocol; Pi missing-binary notice; Hermes exit code and output bound; OpenCode only if
     the toast check passes.
   - e2e: `tui_test.go` stays as it is for Codex (the limit doesn't change; the template does, so its trust
     flow runs once); P6 message with one agent; `TestUpgradeWithoutSetup` still passes.
2. **Test isolation, fixtures and contract tests:** `COPILOT_HOME` and the variable list in `testenv` (first,
   before any test that runs setup for every agent); the fixture schema, the `pre-v1` and current fixtures,
   and the loader (P5); P4 (`internal/setup/contract_test.go` with `testdata/contract/`,
   `internal/cli/contract_test.go`); the P1 section in `docs/spec.md`.
3. **CLI notices:** P7 (`Agent.Check`, `extfile.Owned`, `extfile.CheckSetup`, `setup.json`, `setupNotices`),
   `doctor` reusing `Check`.
4. **First public release** = version 1: `releases.json` with the release (P5), `TestUpgradeFromRelease` and
   `TestCodexUpgradeKeepsTrust` (the e2e tests on the fixtures of step 2); release checklist with the fixture
   step and the release-notes rule.
5. **Distribution:** Homebrew formula with `caveats` (P9), README install section.

## Open questions

For the author:

1. P2.4: is a feature planned that needs another event (per prompt, compaction)? If yes, decide now whether to
   add it with the freeze, to avoid an additive change right after the release.
2. P9: keep or delete the pre-release `Legacy` cases at the first public release? (Recommended: keep.)
3. P7: is a notice on every CLI command acceptable, or only on `files`, `show`, `explain`, `doctor`, `sync`?

For verification during implementation:

4. Resumed and forked Claude and Codex sessions: what do they get (P2.4)?
5. OpenCode server plugins: is there a way to show a toast (P3.5)?
6. Which of Cursor, Copilot, Antigravity, Muse have a user-message field in their hook answer (P6)?
7. The cost of 40 hook processes inside Claude Code at session start, if more slots are ever considered.

## Risks

- The freeze is only as good as the decisions in P2 and P3. A missed need is an additive or capacity change,
  handled by P1.
- Golden files can be updated without thinking. The test's doc comment, the checklist and the release-notes
  rule are the guard; a reviewer should ask for the P1 kind of every golden-file change.
- The P6 message is frozen in the template; its wording can't be improved later without a Codex trust
  prompt.
- The skill list is never dropped (`resolve.Result.Fit`), so a session's text can be larger than the agent's
  capacity. That is today's behavior and out of scope here, but it is the reason Codex keeps a finite limit,
  and it should get its own decision (for example, a cap on the skill list).
- Agents can break the contract from their side (S4). `e2e-latest.yml` finds it; it runs only on
  `workflow_dispatch` while the repository is private.
