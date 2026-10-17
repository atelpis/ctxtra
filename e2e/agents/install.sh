#!/bin/sh
# Installs the pinned agent versions into e2e/agents/node_modules, where the
# e2e drivers find them. Exact versions and hashes come from
# package-lock.json. Muse Code isn't on npm: install-muse.mjs puts it in
# e2e/agents/bin, at the version and sha256 in muse.json. With "latest" as
# the argument, it installs the latest releases instead (the nightly check).
# Package install scripts are skipped, except Claude Code's and OpenCode's,
# which only copy their native binary into place. Copilot has none: its
# loader runs the binary of the platform package.
set -eu
cd "$(dirname "$0")"
if [ "${1:-}" = latest ]; then
	npm install --ignore-scripts --no-audit --no-fund --no-save \
		@anthropic-ai/claude-code@latest @openai/codex@latest @earendil-works/pi-coding-agent@latest @opencode/cli@latest \
		@github/copilot@latest
	node install-muse.mjs latest
else
	npm ci --ignore-scripts --no-audit --no-fund
	node install-muse.mjs
fi
npm rebuild @anthropic-ai/claude-code @opencode/cli
for b in claude codex pi opencode copilot; do
	printf '%s: %s\n' "$b" "$(./node_modules/.bin/$b --version | head -n 1)"
done
printf 'muse: %s\n' "$(./bin/muse --version | head -n 1)"
