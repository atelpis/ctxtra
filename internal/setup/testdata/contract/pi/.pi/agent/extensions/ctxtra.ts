// @ctxtra-managed
// binary: $HOME/bin/ctxtra
// protocol: 1
// Written by ctxtra setup. Adds the instruction files and skills that
// ctxtra binds to the current folder. Safe to delete; `ctxtra setup --remove` removes it.
import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";

const bin = join(homedir(), "bin/ctxtra");

const missing = {};

function run(ev: string, cwd: string): Promise<any> {
	return new Promise((resolve) => {
		try {
			const child = execFile(bin, ["hook", "pi", ev], { encoding: "utf8", timeout: 10000, maxBuffer: 64 * 1024 * 1024 },
				(err: any, stdout) => {
					if (err) return resolve(err.code === "ENOENT" || err.code === "EACCES" ? missing : {});
					try {
						resolve(JSON.parse(stdout));
					} catch {
						resolve({});
					}
				});
			child.stdin?.on("error", () => {});
			child.stdin?.end(JSON.stringify({ cwd, ctxtra_plugin: 1 }));
		} catch {
			resolve({});
		}
	});
}

export default function ctxtra(pi: any) {
	// Tells the user once per session what ctxtra added.
	pi.on("session_start", async (_event: any, ctx: any) => {
		const answer = await run("session_start", ctx.cwd);
		if (!ctx.hasUI) return;
		if (answer === missing) ctx.ui.notify("ctxtra: the ctxtra binary that this hook runs is missing; reinstall ctxtra, or run ctxtra setup --agent pi with the new binary", "warning");
		else if (answer.message) ctx.ui.notify(answer.message, "info");
	});
	// Skills are loaded as native Pi skills, also available as /skill: commands.
	pi.on("resources_discover", async (event: any) => ({ skillPaths: (await run("resources_discover", event.cwd)).skills ?? [] }));
	pi.on("before_agent_start", async (event: any, ctx: any) => {
		const files: { path: string; content: string }[] = (await run("before_agent_start", ctx.cwd)).files ?? [];
		if (files.length === 0) return;
		const opts = event.systemPromptOptions;
		if (!opts) return;
		const known = new Set((opts.contextFiles ?? []).map((f: any) => f.path));
		opts.contextFiles = [...(opts.contextFiles ?? []), ...files.filter((f) => !known.has(f.path))];
	});
}
