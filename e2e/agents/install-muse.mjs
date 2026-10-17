// Installs Muse Code into e2e/agents/bin/muse, where the e2e driver finds
// it. Muse isn't on npm, so the pinned version and its checksums are in
// muse.json. The binary comes from Meta's versioned release manifest, the one
// Muse's own launcher uses, and must match the pinned sha256.
//
//   node install-muse.mjs           the version in muse.json
//   node install-muse.mjs latest    the current stable release, checked against its manifest
//   node install-muse.mjs update    pin the current stable release in muse.json, then install it
import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const lockPath = join(here, "muse.json");
const channelURL = "https://api.meta.ai/muse-code/channels/muse-stable";
const manifestURL = (v) =>
	`https://lookaside.facebook.com/lookaside/muse/download/?channel=muse&version=${encodeURIComponent(v)}&file=manifest.json`;
const platforms = { "darwin-arm64": "aarch64_macos", "darwin-x64": "x86_macos", "linux-arm64": "aarch64_linux", "linux-x64": "x86_linux" };

async function get(url) {
	const res = await fetch(url, { headers: { "User-Agent": "ctxtra-e2e" } });
	if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`);
	return res;
}

async function manifest(version) {
	const m = await (await get(manifestURL(version))).json();
	if (m.version !== version || m.checksum_algorithm !== "sha256") throw new Error(`unexpected manifest for ${version}`);
	return m;
}

const mode = process.argv[2] ?? "pinned";
let version, sums;
if (mode === "pinned") {
	({ version, sha256: sums } = JSON.parse(readFileSync(lockPath, "utf8")));
} else if (mode === "latest" || mode === "update") {
	version = (await (await get(channelURL)).json()).version;
	const m = await manifest(version);
	sums = Object.fromEntries(Object.values(platforms).map((p) => [p, m.artifacts[p]?.checksum]));
	if (mode === "update") writeFileSync(lockPath, JSON.stringify({ version, sha256: sums }, null, 2) + "\n");
} else {
	throw new Error(`unknown mode ${mode}: pinned, latest or update`);
}

const platform = platforms[`${process.platform}-${process.arch}`];
if (!platform) throw new Error(`Muse Code has no build for ${process.platform}-${process.arch}`);
const artifact = (await manifest(version)).artifacts[platform];
const want = sums[platform];
if (!artifact || !/^[0-9a-f]{64}$/.test(want ?? "")) throw new Error(`no ${platform} build of Muse Code ${version}`);
const body = Buffer.from(await (await get(artifact.url)).arrayBuffer());
const got = createHash("sha256").update(body).digest("hex");
if (got !== want) throw new Error(`Muse Code ${version} ${platform}: sha256 ${got}, want ${want}`);

const bin = join(here, "bin", "muse");
mkdirSync(dirname(bin), { recursive: true });
writeFileSync(bin + ".tmp", body);
chmodSync(bin + ".tmp", 0o755);
renameSync(bin + ".tmp", bin);
