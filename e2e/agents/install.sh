#!/bin/sh
# Installs the pinned agent versions into e2e/agents/node_modules, where the
# e2e drivers find them. Exact versions and hashes come from
# package-lock.json. Muse Code isn't on npm: install-muse.mjs puts it in
# e2e/agents/bin, at the version and sha256 in muse.json. With "latest" as
# the argument, it installs the latest releases instead (the nightly check).
# Package install scripts are skipped, except Claude Code's and OpenCode's,
# which only copy their native binary into place. Copilot has none: its
# loader runs the binary of the platform package.
#
# The Antigravity CLI isn't on npm: its build for this platform is
# downloaded into bin/agy, checked against the SHA-512 in antigravity.lock
# (or, with "latest", in Google's release manifest), and its own install
# script, which edits shell profiles, isn't run.
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

case "$(uname -s)-$(uname -m)" in
Darwin-arm64) platform=darwin_arm64 ;;
Darwin-x86_64) platform=darwin_amd64 ;;
Linux-x86_64) platform=linux_amd64 ;;
Linux-aarch64 | Linux-arm64) platform=linux_arm64 ;;
*) echo "install.sh: no Antigravity CLI build for $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac
if [ "${1:-}" = latest ]; then
	m=$(curl -fsSL "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/$platform.json")
	field() { printf '%s' "$m" | sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'; }
	url=$(field url) sha512=$(field sha512)
else
	url=$(awk -v p="$platform" '$1 == p { print $2 }' antigravity.lock)
	sha512=$(awk -v p="$platform" '$1 == p { print $3 }' antigravity.lock)
fi
if [ -z "$url" ] || [ -z "$sha512" ]; then
	echo "install.sh: no Antigravity CLI build for $platform" >&2
	exit 1
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/agy.tar.gz" "$url"
if command -v sha512sum >/dev/null; then
	got=$(sha512sum "$tmp/agy.tar.gz" | cut -d' ' -f1)
else
	got=$(shasum -a 512 "$tmp/agy.tar.gz" | cut -d' ' -f1)
fi
if [ "$got" != "$sha512" ]; then
	echo "install.sh: $url: SHA-512 $got, want $sha512" >&2
	exit 1
fi
tar -xzf "$tmp/agy.tar.gz" -C "$tmp" antigravity
mkdir -p bin
mv "$tmp/antigravity" bin/agy
chmod +x bin/agy

for b in node_modules/.bin/claude node_modules/.bin/codex node_modules/.bin/pi node_modules/.bin/opencode node_modules/.bin/copilot bin/agy; do
	printf '%s: %s\n' "${b##*/}" "$(./$b --version | head -n 1)"
done
printf 'muse: %s\n' "$(./bin/muse --version | head -n 1)"
