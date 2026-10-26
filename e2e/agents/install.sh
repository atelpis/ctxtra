#!/bin/sh
# Installs the pinned agent versions into e2e/agents/node_modules, where the
# e2e drivers find them. Exact versions and hashes come from
# package-lock.json. Muse Code isn't on npm: install-muse.mjs puts it in
# e2e/agents/bin, at the version and sha256 in muse.json. With "latest" as
# the argument, it installs the latest releases instead (the nightly check):
# for DSH that is its "next" tag, which its newest releases get, while
# "latest" stays on an older one. Package install scripts are skipped,
# except Claude Code's and OpenCode's, which only copy their native binary
# into place. Copilot has none: its loader runs the binary of the platform
# package.
#
# The Antigravity CLI isn't on npm: its build for this platform is
# downloaded into bin/agy, checked against the SHA-512 in antigravity.lock
# (or, with "latest", in Google's release manifest), and its own install
# script, which edits shell profiles, isn't run.
#
# Hermes, a Python app, isn't published as a package: its source at the
# commit in hermes.ref goes into hermes/, with the dependencies and hashes
# of its uv.lock in hermes/.venv (with "latest", the latest release tag).
# It needs Python 3.11 to 3.13: the first one found in PATH, or
# $E2E_PYTHON.
set -eu
cd "$(dirname "$0")"
hermes_repo=https://github.com/NousResearch/hermes-agent.git
if [ "${1:-}" = latest ]; then
	npm install --ignore-scripts --no-audit --no-fund --no-save \
		@anthropic-ai/claude-code@latest @openai/codex@latest @earendil-works/pi-coding-agent@latest @opencode/cli@latest \
		@github/copilot@latest @deepseek-ai/dsh@next
	node install-muse.mjs latest
	hermes=$(git ls-remote --tags --refs "$hermes_repo" | sed 's#.*refs/tags/##' | sort -V | tail -n 1)
else
	npm ci --ignore-scripts --no-audit --no-fund
	node install-muse.mjs
	read -r hermes _ <hermes.ref
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

py=${E2E_PYTHON:-}
if [ -z "$py" ]; then
	for p in python3.13 python3.12 python3.11 python3; do
		if command -v "$p" >/dev/null && "$p" -c 'import sys; sys.exit(not (3, 11) <= sys.version_info[:2] <= (3, 13))'; then
			py=$p
			break
		fi
	done
fi
if [ -z "$py" ]; then
	echo "install.sh: Hermes needs Python 3.11 to 3.13; set E2E_PYTHON" >&2
	exit 1
fi
rm -rf hermes
git init -q hermes
git -C hermes fetch -q --depth 1 "$hermes_repo" "$hermes"
git -C hermes -c advice.detachedHead=false checkout -q FETCH_HEAD
"$py" -m venv hermes/.uv
hermes/.uv/bin/pip install --quiet --disable-pip-version-check --require-hashes --only-binary=:all: -r requirements.txt
(cd hermes && UV_PYTHON="$py" UV_PYTHON_DOWNLOADS=never .uv/bin/uv sync --quiet --frozen --no-dev)

for b in node_modules/.bin/claude node_modules/.bin/codex node_modules/.bin/pi node_modules/.bin/opencode node_modules/.bin/copilot bin/agy \
	hermes/.venv/bin/hermes node_modules/.bin/dsh; do
	printf '%s: %s\n' "${b##*/}" "$(./$b --version | head -n 1)"
done
printf 'muse: %s\n' "$(./bin/muse --version | head -n 1)"
