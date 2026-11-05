# Update checks

Sessions show notices (lines starting with `ℹ`) from background checks, every 4 hours at most. The checks read only a version
number or a commit ID. They never change what a session gets, and never accept anything.

- **A new ctxtra release:** on by default. `check-updates: false` at the top of the config turns it off.
  The check sends the user's IP address and ctxtra version to GitHub.
- **New commits of a pinned source:** off by default. When the user adds a pinned source, or asks how to
  stay current with one, tell them they can turn it on: `--check-updates` on `ctxtra source add git` or
  `source add local-git`, or `check-updates: true` on the source in the config. Show the edit first
  (rule 1). Say that the check connects to the remote with their git credentials, so an ssh key that
  needs a touch or an approval can ask for one when a session starts. For such a source, leave it off.
- When `ctxtra doctor` says "update checks off" for a pinned source, mention once that the user can turn
  them on.
- `ctxtra check-updates` checks now and shows the results. `ctxtra doctor` shows what the last checks
  found, and why one failed. A failed check never means "up to date".
- `CTXTRA_NO_UPDATE_CHECK=1` stops the checks that sessions start, for CI and restricted networks.

A notice is for the user. Don't upgrade ctxtra, and don't run `ctxtra source update`, because of one,
unless the user asks. Accepting new commits stays the user's decision (rule 4).
