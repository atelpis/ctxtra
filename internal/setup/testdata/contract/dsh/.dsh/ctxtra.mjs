// @ctxtra-managed
// binary: $HOME/bin/ctxtra
// protocol: 1
// Written by ctxtra setup and loaded by its row in cordis.patch.yml. Adds the
// instruction files and skills that ctxtra binds to the session's folder.
// `ctxtra setup --remove` removes both.
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { homedir } from "node:os";
import { join } from "node:path";

const bin = join(homedir(), "bin/ctxtra");

function run(ev, cwd, signal) {
	return new Promise((resolve) => {
		try {
			const child = execFile(bin, ["hook", "dsh", ev], { encoding: "utf8", timeout: 10000, maxBuffer: 64 * 1024 * 1024, signal },
				(err, stdout) => {
					if (err) return resolve({});
					try {
						resolve(JSON.parse(stdout) ?? {});
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

// The session's folder: the workingDirectory service where DSH has one
// (0.2.1 and later), else the folder the session started in.
async function folder(ctx, agent, signal) {
	try {
		const wd = ctx.get("workingDirectory");
		if (wd) return await wd.ensure(agent, signal);
	} catch {}
	return agent.session?.header?.cwd ?? process.cwd();
}

export const name = "ctxtra";

// The text for each new agent's first step.
const pending = new WeakMap();

// The listeners never throw: DSH fails the agent's creation, or the step,
// when they do. Without an answer from ctxtra (no binary, timeout) the
// plugin adds nothing.
export function apply(ctx) {
	ctx.on("agent/created", async ({ agent, source, signal }) => {
		if (source === "resume") return;
		try {
			const ev = agent.session?.header?.parentSession ? "subagent" : source;
			const { text } = await run(ev, await folder(ctx, agent, signal), signal);
			if (text) pending.set(agent, { id: randomUUID(), role: "user", content: [{ type: "text", text }], source: { kind: "ctxtra" } });
		} catch {}
	});
	// Added at the end of the first step's messages, after the repository's
	// AGENTS.md, which DSH adds in this same waterfall. A step without
	// messages of its own keeps the text for the next one.
	ctx.on("agent/pre-step", async ({ agent }, next) => {
		const decision = await next();
		try {
			const message = pending.get(agent);
			if (!message || decision?.kind !== "enter" || !Array.isArray(decision.messages) || decision.messages.length === 0) return decision;
			pending.delete(agent);
			return { ...decision, messages: [...decision.messages, message] };
		} catch {
			return decision;
		}
	});
}
