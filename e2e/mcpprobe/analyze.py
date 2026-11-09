"""Prints a summary of the mcpprobe results: python3 -I analyze.py OUT [agent...]"""
import json, os, sys

out = sys.argv[1]
agents = sys.argv[2:] or sorted(d for d in os.listdir(out) if os.path.isdir(os.path.join(out, d)) and d != "bin")
order = ["top", "top-default", "sub", "worktree", "resume-first", "resume", "resume-elsewhere-first",
         "resume-elsewhere", "concurrent-0", "concurrent-1"]


def short(p):
    if not isinstance(p, str):
        return p
    i = p.find("/home/")
    return "~" + p[i + 5:] if i >= 0 else p


for a in agents:
    d = os.path.join(out, a)
    print("=" * 20, a)
    for sc in order:
        p = os.path.join(d, sc + ".json")
        if not os.path.exists(p):
            continue
        r = json.load(open(p))
        print(f"-- {sc}: dir={short(r['dir'])} pid={r['pid']} err={r.get('error')!r} dur={r.get('duration')}")
        hooks = r.get("hooks") or []
        hk = sorted({(short((h.get("input") or {}).get("cwd")), short(h.get("cwd")), (h.get("input") or {}).get("source"),
                      (h.get("input") or {}).get("hook_event_name")) for h in hooks}, key=str)
        print(f"   hooks: n={len(hooks)} (input.cwd, proc cwd, source, event) {hk}")
        if hooks:
            print("   hook chain:", [x["command"][:60] for x in hooks[0].get("ancestors", [])[:4]])
        starts = [m for m in r.get("mcp") or [] if m["event"] == "start"]
        print(f"   mcp starts: {len(starts)}")
        for s in starts:
            print("     cwd:", short(s["cwd"]), "env:", {k: short(v) for k, v in s["env"].items() if k not in ("TMPDIR",)})
            print("     chain:", [x["command"][:60] for x in s.get("ancestors", [])[:4]])
        for m in r.get("mcp") or []:
            ev, meth = m["event"], m.get("method")
            if ev == "client_message" and meth == "initialize":
                pr = m["params"]
                print("   initialize: proto", pr.get("protocolVersion"), "caps", json.dumps(pr.get("capabilities")),
                      "client", (pr.get("clientInfo") or {}).get("name"), (pr.get("clientInfo") or {}).get("version"))
            elif ev == "client_message" and meth == "tools/call":
                print("   tools/call:", m["params"].get("name"))
            elif ev == "client_message" and meth not in ("notifications/initialized",):
                print("   client:", meth)
            elif ev == "client_response":
                print("   response to", m.get("for"), ":", json.dumps(m.get("result") or m.get("error"))[:300])
            elif ev in ("sent_list_changed", "server_request", "stdin_closed"):
                print("   ", ev, m.get("method", ""))
            elif ev == "tools_listed":
                print("   tools_listed late=", m.get("late"))
        prev, prevm = None, None
        for i, q in enumerate(r.get("requests") or []):
            names = [t["Namespace"] + ("/" if t["Namespace"] else "") + t["Name"] for t in (q.get("probe_tools") or [])]
            print(f"   req{i} {q['role']} reply={q['reply']} tools={q['tool_count']} markers={q['markers']}")
            if names != prev:
                print("      probe tools:", names)
                prev = names
            if q.get("probe_mentions") and i == 0:
                print("      mentions:", q["probe_mentions"])
        tailtxt = (r.get("stderr_tail") or "")[-600:]
        if r.get("error") and tailtxt:
            print("   stderr:", tailtxt.replace("\n", " | "))
