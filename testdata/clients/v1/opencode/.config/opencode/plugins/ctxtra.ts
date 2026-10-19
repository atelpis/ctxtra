// @ctxtra-managed
// binary: $HOME/bin/ctxtra
// protocol: 1
// Written by ctxtra setup. Adds the instruction files and skills that
// ctxtra binds to the session's folder. Safe to delete; `ctxtra setup --remove` removes it.
import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";

const bin = join(homedir(), "bin/ctxtra");

type Skill = { name: string; description: string; path: string; content: string };
type Answer = { text?: string; skills?: Skill[] };

function run(ev: string, cwd: string): Promise<Answer> {
	return new Promise((resolve) => {
		try {
			const child = execFile(bin, ["hook", "opencode", ev], { encoding: "utf8", timeout: 10000, maxBuffer: 64 * 1024 * 1024 },
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

// How many sessions' answers the plugin keeps; the oldest goes first.
const maxSessions = 256;

// The plugin never throws: OpenCode would show a plugin error, or fail the
// request. Without an answer from ctxtra (no binary, timeout) it adds
// nothing, keeps the skills it has, and asks again on the next request.
export default {
	id: "ctxtra",
	async setup(ctx: any) {
		try {
			const here: string = ctx.location?.directory ?? process.cwd();
			let skills: Skill[] = (await run("setup", here)).skills ?? [];
			// A skill of the user's own with the same name wins.
			await ctx.skill.transform((editor: any) => {
				for (const s of skills) {
					if (!editor.get(s.name)) editor.add({ id: s.name, ...s });
				}
			});
			const sessions = new Map<string, Promise<Answer>>();
			const answer = (sessionID: string): Promise<Answer> => {
				let p = sessions.get(sessionID);
				if (!p) {
					p = (async () => {
						let info: any;
						try {
							info = await ctx.session.get({ sessionID });
						} catch {}
						const a = await run(info?.parentID ? "subagent" : "context", info?.location?.directory ?? here);
						if (a.text === undefined) sessions.delete(sessionID);
						return a;
					})();
					sessions.set(sessionID, p);
					if (sessions.size > maxSessions) sessions.delete(sessions.keys().next().value!);
				}
				return p;
			};
			await ctx.session.hook("context", async (event: any) => {
				try {
					const a = await answer(event.sessionID);
					if (a.text) event.system.push({ type: "text", text: a.text });
					if (a.skills && JSON.stringify(a.skills) !== JSON.stringify(skills)) {
						skills = a.skills;
						await ctx.skill.reload();
					}
				} catch {}
			});
		} catch {}
	},
};
