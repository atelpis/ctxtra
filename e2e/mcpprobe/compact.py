"""One line per run: python3 -I compact.py OUT [agent...]"""
import json, os, sys
out = sys.argv[1]
agents = sys.argv[2:] or ["claude", "codex", "pi", "opencode", "muse", "antigravity"]
order = ["top", "top-default", "sub", "worktree", "resume-first", "resume", "resume-elsewhere-first",
         "resume-elsewhere", "concurrent-0", "concurrent-1"]
def short(p):
    if not isinstance(p, str): return p
    p = p.replace("file://", "")
    i = p.find("/home/")
    return "~" + p[i + 5:] if i >= 0 else p
for a in agents:
    print("=" * 10, a)
    for sc in order:
        p = os.path.join(out, a, sc + ".json")
        if not os.path.exists(p): continue
        r = json.load(open(p))
        hooks = r.get("hooks") or []
        hk = sorted({(short((h.get("input") or {}).get("cwd")), (h.get("input") or {}).get("source") or (h.get("input") or {}).get("hook_event_name"),
                      h["ancestors"][0]["pid"] == r["pid"] if h.get("ancestors") else None) for h in hooks}, key=str)
        starts = [m for m in r.get("mcp") or [] if m["event"] == "start"]
        roots = [json.dumps([short(x.get("uri")) for x in (m.get("result") or {}).get("roots", [])]) if m.get("result") else json.dumps(m.get("error"))
                 for m in r.get("mcp") or [] if m["event"] == "client_response" and m.get("for") == "roots/list"]
        ms = [(short(s["cwd"]), "parent=agent" if s["ancestors"] and s["ancestors"][0]["pid"] == r["pid"] else "parent=" + (s["ancestors"][0]["command"][-40:] if s["ancestors"] else "?"),
               {k: short(v) for k, v in s["env"].items() if k not in ("TMPDIR", "NoDefaultCurrentDirectoryInExePath", "PI_CODING_AGENT_DIR", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "DSH_HOME", "COPILOT_HOME")}) for s in starts]
        calls = [m["params"]["name"] for m in r.get("mcp") or [] if m.get("method") == "tools/call"]
        print(f"{sc:24} dir={short(r['dir']):26} err={r.get('error')} hooks={hk} mcp={ms} roots={roots} calls={len(calls)}")
