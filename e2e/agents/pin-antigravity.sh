#!/bin/sh
# Pins the latest Antigravity CLI release in antigravity.lock: its version,
# and the download URL and SHA-512 per platform, from Google's release
# manifests (the ones its install script reads). The CLI isn't on npm, so
# Dependabot can't update it.
set -eu
cd "$(dirname "$0")"
base=https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests
field() { sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'; }
out=$(mktemp)
trap 'rm -f "$out"' EXIT
version=
for p in darwin_arm64 darwin_amd64 linux_amd64 linux_arm64; do
	m=$(curl -fsSL "$base/$p.json")
	v=$(printf '%s' "$m" | field version)
	if [ -n "$version" ] && [ "$v" != "$version" ]; then
		echo "pin-antigravity: $p is at $v, the others at $version; try again later" >&2
		exit 1
	fi
	version=$v
	printf '%s %s %s\n' "$p" "$(printf '%s' "$m" | field url)" "$(printf '%s' "$m" | field sha512)" >>"$out"
done
{
	echo "# Antigravity CLI $version, written by pin-antigravity.sh: platform, URL, SHA-512."
	cat "$out"
} >antigravity.lock
echo "pinned Antigravity CLI $version"
