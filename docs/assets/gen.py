"""Generates the README visuals in docs/assets. Run from the repository root: python3 docs/assets/gen.py docs/assets
The social preview is rendered from social-preview.svg with:
rsvg-convert -w 1280 -h 640 docs/assets/social-preview.svg -o docs/assets/social-preview.png

Each picture except the terminal demo comes in a dark and a light variant; the
README picks one with <picture> and prefers-color-scheme, which GitHub follows.
"""
import sys
from html import escape

OUT = sys.argv[1]

MONO = "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace"
SANS = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif"

THEMES = {
    "dark": dict(
        BG="#0B0F17", PANEL="#121926", EDGE="#243045", GRID="#1A2230", TEXT="#E6EDF3", MUTED="#9AA6B8",
        DIM="#6B778A", VIOLET="#A78BFA", CYAN="#22D3EE", AMBER="#FBBF24", LIME="#A3E635",
        RED="#F87171", GREEN="#4ADE80", TINT="0.14", EDGE_OP="0.6", LINE="#2A3548", HALO="url(#g)"),
    "light": dict(
        BG="#FFFFFF", PANEL="#F6F8FA", EDGE="#D0D7DE", GRID="#E6EAEF", TEXT="#1F2328", MUTED="#57606A",
        DIM="#6E7781", VIOLET="#7C3AED", CYAN="#0E7490", AMBER="#C2410C", LIME="#4D7C0F",
        RED="#CF222E", GREEN="#1A7F37", TINT="0.10", EDGE_OP="0.75", LINE="#8C959F", HALO="#C9D1D9"),
}

# One color per bound item, the same in every picture.
FILE_NAMES = ["me.md", "go-style.md", "billing.md", "deploy"]
FILE_COLORS = ["VIOLET", "CYAN", "AMBER", "LIME"]


def write(name, body, w, h, title):
    svg = (
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" '
        f'role="img" font-family="{SANS}">\n<title>{escape(title)}</title>\n{body}\n</svg>\n'
    )
    with open(f"{OUT}/{name}", "w") as f:
        f.write(svg)


def frame(c, w, h, r=18, grid=False):
    s = f'<rect x="0.5" y="0.5" width="{w-1}" height="{h-1}" rx="{r}" fill="{c["BG"]}" stroke="{c["EDGE"]}"/>\n'
    if grid:
        s += (f'<defs><pattern id="grid" width="24" height="24" patternUnits="userSpaceOnUse">'
              f'<circle cx="2" cy="2" r="1" fill="{c["GRID"]}"/></pattern></defs>'
              f'<rect x="1" y="1" width="{w-2}" height="{h-2}" rx="{r}" fill="url(#grid)"/>\n')
    return s


def gradient(c):
    return (f'<defs><linearGradient id="g" x1="0" x2="1"><stop offset="0" stop-color="{c["VIOLET"]}"/>'
            f'<stop offset="0.5" stop-color="{c["CYAN"]}"/><stop offset="1" stop-color="{c["LIME"]}"/></linearGradient></defs>')


def label(name):
    return "deploy/ skill" if name == "deploy" else name


# ---------------------------------------------------------------- hero

SESSIONS = [
    ("claude", "~/dev/acme/billing", [0, 1, 2, 3]),
    ("codex", "~/wt/billing-fix", [0, 1, 2, 3]),
    ("pi", "~/dev/side-project", [0, 1]),
    ("cursor-agent", "~/Downloads", [0]),
]


# Packages on the left, sessions on the right; every edge is a binding.
PACKAGES = [
    ("personal", "~/dev/dotfiles", "live", [0, 1]),
    ("team", "acme/team-ctx", "pinned", [2, 3]),
]


HEADLINE = ["Your own layer", "of agent context."]


def hero_body(c, w, h, top=0, animate=True, paths=True):
    """The hero content; top shifts it down for the taller social preview."""
    col = lambda i: c[FILE_COLORS[i]]
    b = [gradient(c)]
    t = top
    # The wordmark is drawn in layers that all hold the full "ctxtra_", so the glyphs line up in any
    # monospace font. The x slowly changes between the color of "ctx" (context) and of "xtra" (extra).
    def wordmark(ct, x, tra, caret, anim=""):
        paint = lambda fill: f'fill="{fill}"' if fill else 'fill="none"'
        return (f'<text x="62" y="{t+140}" font-family="{MONO}" font-size="104" font-weight="700" letter-spacing="-3">'
                f'<tspan {paint(ct)}>ct</tspan><tspan {paint(x)}>x</tspan><tspan {paint(tra)}>tra</tspan>'
                f'<tspan {paint(caret)}{f" stroke={chr(34)}{caret}{chr(34)} stroke-width={chr(34)}5{chr(34)}" if caret else ""}>_</tspan>'
                f'{anim}</text>')
    b.append(wordmark("url(#g)", c["TEXT"], c["TEXT"], None))
    if animate:
        b.append(wordmark(None, "url(#g)", None, None,
                          '<animate attributeName="opacity" values="1;1;0;0;1" keyTimes="0;0.4;0.5;0.9;1" dur="6s" repeatCount="indefinite"/>'))
        b.append(wordmark(None, None, None, c["CYAN"],
                          '<animate attributeName="opacity" values="1;0" calcMode="discrete" dur="1.1s" repeatCount="indefinite"/>'))
    else:
        b.append(wordmark(None, "url(#g)", None, c["CYAN"]))
    # the headline sits just above the agent list
    for k, line in enumerate(HEADLINE):
        b.append(f'<text x="64" y="{t + 274 + k*56}" font-size="46" font-weight="700" fill="{c["TEXT"]}">{escape(line)}</text>')
    x = 64
    for name in ["Claude Code", "Codex", "Pi", "OpenCode", "Cursor"]:
        cw = 22 + len(name) * 8.9
        b.append(f'<rect x="{x}" y="{t+362}" width="{cw:.0f}" height="34" rx="17" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>')
        b.append(f'<text x="{x + cw/2:.0f}" y="{t+385}" font-size="15" fill="{c["TEXT"]}" text-anchor="middle">{name}</text>')
        x += cw + 8
    b.append(f'<text x="{x+2}" y="{t+385}" font-size="16" fill="{c["MUTED"]}">+ more</text>')

    px, pw = 640, 232
    cx, cw_ = px + 14, pw - 28
    sx, sw, sh = 1020, 224, 76
    sys_ = [t + y for y in (50, 140, 230, 320)]
    boxes = [t + 50, t + 222]
    chip_y = {}
    for (kind, where, mode, items), by in zip(PACKAGES, boxes):
        b.append(f'<rect x="{px}" y="{by}" width="{pw}" height="160" rx="12" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>')
        b.append(f'<text x="{px+14}" y="{by+24}" font-size="14" font-weight="700" fill="{c["MUTED"]}" letter-spacing="1">{kind.upper()}</text>')
        tag = c["GREEN"] if mode == "live" else c["CYAN"]
        b.append(f'<text x="{px+pw-14}" y="{by+24}" font-family="{MONO}" font-size="13" fill="{tag}" text-anchor="end">{mode}</text>')
        b.append(f'<text x="{px+14}" y="{by+46}" font-family="{MONO}" font-size="14" fill="{c["DIM"]}">{escape(where)}</text>')
        for n, i in enumerate(items):
            chip_y[i] = by + 62 + n * 46
    # bindings: one edge per file and session that gets it
    edges = [(i, j, sys_[j] + sh / 2 + (n - (len(got) - 1) / 2) * 9)
             for j, (_, _, got) in enumerate(SESSIONS) for n, i in enumerate(got)]
    for i, j, y1 in edges:
        y0 = chip_y[i] + 19
        b.append(f'<path d="M{cx+cw_} {y0} C{cx+cw_+80} {y0} {sx-80} {y1} {sx} {y1}" fill="none" stroke="{col(i)}" stroke-opacity="{c["EDGE_OP"]}" stroke-width="2"/>')
    if animate:
        for k, (i, j, y1) in enumerate(edges):
            y0 = chip_y[i] + 19
            d = f"M{cx+cw_} {y0} C{cx+cw_+80} {y0} {sx-80} {y1} {sx} {y1}"
            begin = (k * 0.53) % 2.6
            b.append(
                f'<circle r="4" fill="{col(i)}" opacity="0">'
                f'<animateMotion path="{d}" dur="2.6s" begin="{begin:.2f}s" repeatCount="indefinite"/>'
                f'<animate attributeName="opacity" values="0;1;1;0" keyTimes="0;0.1;0.9;1" dur="2.6s" begin="{begin:.2f}s" repeatCount="indefinite"/>'
                f'</circle>')
    for i, y in chip_y.items():
        b.append(f'<rect x="{cx}" y="{y}" width="{cw_}" height="38" rx="9" fill="{c["BG"]}" stroke="{c["EDGE"]}"/>')
        b.append(f'<circle cx="{cx+18}" cy="{y+19}" r="6" fill="{col(i)}"/>')
        b.append(f'<text x="{cx+34}" y="{y+25}" font-family="{MONO}" font-size="16" fill="{c["TEXT"]}">{label(FILE_NAMES[i])}</text>')
    b.append(f'<text x="{(cx+cw_+sx)/2}" y="{t+34}" font-family="{MONO}" font-size="14" fill="{c["DIM"]}" text-anchor="middle">bindings</text>')
    b.append(f'<text x="{sx}" y="{t+34}" font-family="{MONO}" font-size="14" fill="{c["DIM"]}">sessions</text>')
    b.append(f'<text x="{px}" y="{t+34}" font-family="{MONO}" font-size="14" fill="{c["DIM"]}">packages</text>')
    for (agent, path, got), sy in zip(SESSIONS, sys_):
        b.append(f'<rect x="{sx}" y="{sy}" width="{sw}" height="{sh}" rx="10" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>')
        if paths:
            b.append(f'<text x="{sx+14}" y="{sy+24}" font-family="{MONO}" font-size="16" font-weight="700" fill="{c["TEXT"]}">{agent}</text>')
            b.append(f'<text x="{sx+14}" y="{sy+46}" font-family="{MONO}" font-size="15" fill="{c["MUTED"]}">{escape(path)}</text>')
            dots_x, dots_y = sx + 20, sy + 62
        else:
            b.append(f'<text x="{sx+16}" y="{sy+sh/2+8}" font-family="{MONO}" font-size="22" font-weight="700" fill="{c["TEXT"]}">{agent}</text>')
            dots_x, dots_y = sx + sw - 20 - (len(got) - 1) * 17, sy + sh / 2
        for n, i in enumerate(got):
            b.append(f'<circle cx="{dots_x+n*17}" cy="{dots_y}" r="5.5" fill="{col(i)}"/>')
    return "\n".join(b)


def hero(theme):
    c = THEMES[theme]
    w, h = 1280, 432
    write(f"hero-{theme}.svg", frame(c, w, h, grid=True) + hero_body(c, w, h), w, h,
          "ctxtra: your own layer of agent context. Personal and team instruction files and skills, bound to agent sessions")


def social():
    """1280x640 for the GitHub social preview; rendered to PNG, so no animation."""
    c = THEMES["dark"]
    w, h = 1280, 640
    body = (f'<rect width="{w}" height="{h}" fill="{c["BG"]}"/>'
            f'<defs><pattern id="grid" width="24" height="24" patternUnits="userSpaceOnUse">'
            f'<circle cx="2" cy="2" r="1" fill="{c["GRID"]}"/></pattern></defs>'
            f'<rect width="{w}" height="{h}" fill="url(#grid)"/>'
            + hero_body(c, w, h, top=96, animate=False, paths=False)
            + f'<text x="64" y="{h-48}" font-family="{MONO}" font-size="20" fill="{c["MUTED"]}">github.com/atelpis/ctxtra</text>')
    write("social-preview.svg", body, w, h, "ctxtra")


# ---------------------------------------------------------------- demo (dark in both themes: it is a terminal)

def demo():
    """Four panes, one session each, with the summary that ctxtra shows at session start."""
    c = THEMES["dark"]
    w, ph = 1100, 260  # ph: pane height
    h = 46 + 2 * ph
    T = 18.0  # loop length, seconds
    cw = 9.0  # monospace advance at 15px
    lh = 22
    # The bindings of the README's example config, one color per binding.
    personal = [("preferences.md", "VIOLET")], [("review", "VIOLET")]
    work = [("instructions.md", "CYAN"), ("go-style.md", "CYAN")], [("deploy", "CYAN")]
    billing = [("billing.md", "AMBER"), ("billing-runbook.md", "AMBER")], [("billing", "AMBER")]

    def bound(*bindings):
        return [f for fs, _ in bindings for f in fs], [k for _, ks in bindings for k in ks]

    scenes = [
        ("~/dev/acme/billing", "claude", "# remote is github.com/acme/billing", bound(personal, work)),
        ("~/wt/billing-fix", "codex", "# a worktree of it, outside ~/dev", bound(personal, work)),
        ("~/dev/acme/mono/services/billing", "pi", "# github.com/acme/mono, in services/billing", bound(personal, billing)),
        ("~/Downloads", "claude", "# global only", bound(personal)),
    ]
    b = [frame(c, w, h, 14)]
    b.append(f'<rect x="1" y="1" width="{w-2}" height="44" rx="13" fill="{c["PANEL"]}"/>')
    b.append(f'<rect x="1" y="30" width="{w-2}" height="15" fill="{c["PANEL"]}"/>')
    b.append(f'<line x1="1" y1="45" x2="{w-1}" y2="45" stroke="{c["EDGE"]}"/>')
    b.append(f'<line x1="{w/2}" y1="45" x2="{w/2}" y2="{h-1}" stroke="{c["EDGE"]}"/>')
    b.append(f'<line x1="1" y1="{45+ph}" x2="{w-1}" y2="{45+ph}" stroke="{c["EDGE"]}"/>')
    for i, dot in enumerate(["#FF5F57", "#FEBC2E", "#28C840"]):
        b.append(f'<circle cx="{26+i*22}" cy="23" r="6.5" fill="{dot}"/>')
    b.append(f'<text x="{w/2}" y="28" font-size="14" fill="{c["MUTED"]}" text-anchor="middle">one config, four sessions</text>')

    def anim(attr, times, values):
        kt = ";".join(f"{t/T:.4f}" for t in times)
        return (f'<animate attributeName="{attr}" values="{";".join(values)}" keyTimes="{kt}" '
                f'dur="{T}s" repeatCount="indefinite"/>')

    fade_out = (T - 0.9, T - 0.3)

    def appear(t, to="1"):
        return anim("opacity", [0, t, t + 0.01, fade_out[0], fade_out[1], T], ["0", "0", to, to, "0", "0"])

    def text(x, y, body, t):
        return f'<text x="{x}" y="{y}" font-family="{MONO}" font-size="15" xml:space="preserve">{body}{appear(t)}</text>'

    m = c["MUTED"]
    start = 0.6
    for s, (cwd, agent, note, (files, skills)) in enumerate(scenes):
        x0, y = 28 + (s % 2) * w / 2, 45 + (s // 2) * ph + 34
        cmd = f"cd {cwd} && {agent}"
        t0, t1 = start, start + 0.045 * len(cmd)
        t_out = t1 + 0.45
        b.append(text(x0, y, f'<tspan fill="{c["DIM"]}">{escape(note)}</tspan>', t0 - 0.3))
        # the command, revealed by a growing clip
        full = cw * (len(cmd) + 2) + 6
        b.append(f'<clipPath id="c{s}"><rect x="{x0}" y="{y+lh-17}" height="{lh+4}" width="{full}">'
                 + anim("width", [0, t0, t1, T], ["0", "0", f"{full}", f"{full}"]) + "</rect></clipPath>")
        b.append(f'<g clip-path="url(#c{s})">'
                 f'<text x="{x0}" y="{y+lh}" font-family="{MONO}" font-size="15" xml:space="preserve">'
                 f'<tspan fill="{c["GREEN"]}">❯</tspan> <tspan fill="{c["TEXT"]}">cd </tspan><tspan fill="{c["CYAN"]}">{cwd}</tspan>'
                 f'<tspan fill="{c["TEXT"]}"> &amp;&amp; </tspan><tspan fill="{c["TEXT"]}" font-weight="700">{agent}</tspan></text></g>')
        # ctxtra's summary, as the agent shows it, each name in its color
        def count(n, what):
            return f'{n} {what}{"s" if n != 1 else ""}'

        def items(names):
            return [f'<tspan fill="{m}">- </tspan><tspan fill="{c[col]}">{name}</tspan>' for name, col in names]

        head = "ctxtra added " + " and ".join(
            [count(len(files), "instruction")] + ([count(len(skills), "skill")] if skills else []))
        rows = [f'<tspan fill="{c["GREEN"]}">✓</tspan> <tspan fill="{c["TEXT"]}">{head}</tspan>',
                f'<tspan fill="{m}">Instructions:</tspan>', *items(files)]
        if skills:
            rows += [f'<tspan fill="{m}">Skills:</tspan>', *items(skills)]
        for k, row in enumerate(rows):
            b.append(text(x0 + cw * 2, y + lh * (k + 2) + 6, row, t_out + 0.06 * k))
        start = t_out + 2.6
    write("demo.svg", "\n".join(b), w, h, "ctxtra demo: four sessions in four folders get the files that the example config binds to them")


# ---------------------------------------------------------------- skill chat

def skill_chat(theme):
    """A chat with an agent that has the ctxtra skill: it shows each change first, and answers why."""
    c = THEMES[theme]
    w, h = 1100, 522
    T = 17.0
    b = [frame(c, w, h, 14)]
    b.append(f'<rect x="1" y="1" width="{w-2}" height="44" rx="13" fill="{c["PANEL"]}"/>')
    b.append(f'<rect x="1" y="30" width="{w-2}" height="15" fill="{c["PANEL"]}"/>')
    b.append(f'<line x1="1" y1="45" x2="{w-1}" y2="45" stroke="{c["EDGE"]}"/>')
    for i, dot in enumerate(["#FF5F57", "#FEBC2E", "#28C840"]):
        b.append(f'<circle cx="{26+i*22}" cy="23" r="6.5" fill="{dot}"/>')
    b.append(f'<text x="{w/2}" y="28" font-size="14" fill="{c["MUTED"]}" text-anchor="middle">your agent, with the ctxtra skill</text>')

    def appear(t):
        kt = ";".join(f"{x/T:.4f}" for x in [0, t, t + 0.25, T - 0.9, T - 0.3, T])
        return f'<animate attributeName="opacity" values="0;0;1;1;0;0" keyTimes="{kt}" dur="{T}s" repeatCount="indefinite"/>'

    def typing(x, y, a, z):
        """Three dots while the agent works on its answer."""
        kt = ";".join(f"{v/T:.4f}" for v in [0, a, a + 0.01, z, z + 0.01, T])
        dots = "".join(f'<circle cx="{x + 18 + i*14}" cy="{y + 21}" r="3.5" fill="{c["MUTED"]}"/>' for i in range(3))
        return (f'<g opacity="0"><animate attributeName="opacity" values="0;0;1;1;0;0" keyTimes="{kt}" dur="{T}s" repeatCount="indefinite"/>'
                f'<rect x="{x}" y="{y}" width="64" height="42" rx="12" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>{dots}</g>')

    # (who, rows, time); a row is (kind, text): p plain, c a command to run, r a command the agent ran
    msgs = [
        ("you", [("p", "Add my Go instructions to every acme repo")], 0.6),
        ("agent", [("p", "I'll add this binding. OK to run it?"),
                   ("c", "ctxtra binding add go --repo 'github.com/acme/**' --instruction ~/agent-context/go-style.md")], 2.4),
        ("you", [("p", "Yes")], 4.6),
        ("agent", [("p", "✓ Done. New sessions in acme repos get go-style.md, in every agent.")], 5.8),
        ("you", [("p", "Why didn't my billing session get the deploy skill?")], 8.2),
        ("agent", [("r", "ran ctxtra context explain ~/dev/acme/billing"),
                   ("p", "Your team repo is pinned to a1b2c3d, and deploy came in a later commit."),
                   ("p", "To review that commit and accept it, run in your terminal:"),
                   ("c", "ctxtra source update")], 10.4),
    ]
    rh = {"p": 22, "r": 22, "c": 32}
    y = 70
    for who, rows, t in msgs:
        bh = sum(rh[k] for k, _ in rows) + 20
        bw = max((len(s) * (8.4 if k != "p" else 8.0)) + (16 if k == "c" else 0) for k, s in rows) + 32
        if who == "you":
            x = w - 40 - bw
            box = f'<rect x="{x:g}" y="{y}" width="{bw:g}" height="{bh}" rx="12" fill="{c["VIOLET"]}" fill-opacity="{c["TINT"]}" stroke="{c["VIOLET"]}" stroke-opacity="0.5"/>'
        else:
            x = 96
            b.append(typing(x, y, t - 1.0, t))
            box = (f'<rect x="40" y="{y}" width="40" height="40" rx="10" fill="none" stroke="{c["TEXT"]}" stroke-width="2"/>'
                   f'<text x="60" y="{y+26}" font-family="{MONO}" font-size="15" font-weight="700" fill="{c["TEXT"]}" text-anchor="middle">&gt;_</text>'
                   f'<rect x="{x}" y="{y}" width="{bw:g}" height="{bh}" rx="12" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>')
        parts, ry = [box], y + 10
        for k, s in rows:
            if k == "c":
                parts.append(f'<rect x="{x+12:g}" y="{ry+3}" width="{bw-24:g}" height="26" rx="6" fill="{c["BG"]}" stroke="{c["EDGE"]}"/>'
                             f'<text x="{x+22:g}" y="{ry+21}" font-family="{MONO}" font-size="14" fill="{c["CYAN"]}" xml:space="preserve">{escape(s)}</text>')
            elif k == "r":
                parts.append(f'<text x="{x+16:g}" y="{ry+16}" font-family="{MONO}" font-size="13" fill="{c["DIM"]}">{escape(s)}</text>')
            else:
                col = c["GREEN"] if s.startswith("✓") else c["TEXT"]
                parts.append(f'<text x="{x+16:g}" y="{ry+16}" font-size="15" fill="{col}">{escape(s)}</text>')
            ry += rh[k]
        b.append(f'<g opacity="0">{appear(t)}{"".join(parts)}</g>')
        y += bh + 12
    write(f"skill-chat-{theme}.svg", "\n".join(b), w, h,
          "An agent with the ctxtra skill shows the ctxtra binding add command and runs it after you say yes, then explains "
          "why a session didn't get a skill")


# ---------------------------------------------------------------- team sync

def team_sync(theme):
    c = THEMES[theme]
    w, h = 1280, 420
    b = [frame(c, w, h, grid=True)]
    b.append(f'<text x="56" y="62" font-size="15" font-weight="700" letter-spacing="2" fill="{c["CYAN"]}">TEAM CONTEXT, PINNED</text>')
    b.append(f'<text x="56" y="96" font-size="25" font-weight="600" fill="{c["TEXT"]}">New commits reach your agents only after you accept them</text>')
    top, bot = 176, 330
    lx0, lx1 = 300, 1220
    b.append(f'<text x="56" y="{top+6}" font-family="{MONO}" font-size="16" fill="{c["MUTED"]}">acme/team-ctx · main</text>')
    b.append(f'<text x="56" y="{bot+6}" font-family="{MONO}" font-size="16" fill="{c["MUTED"]}">your sessions</text>')
    b.append(f'<line x1="{lx0}" y1="{top}" x2="{lx1}" y2="{top}" stroke="{c["EDGE"]}" stroke-width="3"/>')
    commits = [(360, "abc1234", "first version"), (640, "def5678", "ledger moved to its own service"),
               (980, "1a2b3c4", "now for every session, + run.sh")]
    for x, sha, msg in commits:
        b.append(f'<circle cx="{x}" cy="{top}" r="9" fill="{c["BG"]}" stroke="{c["TEXT"]}" stroke-width="3"/>')
        b.append(f'<text x="{x}" y="{top-22}" font-family="{MONO}" font-size="16" fill="{c["TEXT"]}" text-anchor="middle">{sha}</text>')
        b.append(f'<text x="{x}" y="{top+34}" font-size="15" fill="{c["MUTED"]}" text-anchor="middle">{escape(msg)}</text>')
    mid = (top + bot) / 2 + 10
    for xa, xb, sha, col in [(360, 640, "abc1234", c["VIOLET"]), (640, lx1, "def5678", c["CYAN"])]:
        b.append(f'<rect x="{xa}" y="{bot-15}" width="{xb-xa-4}" height="30" rx="15" fill="{col}" fill-opacity="{c["TINT"]}" stroke="{col}" stroke-opacity="0.6"/>')
        b.append(f'<text x="{xa+16}" y="{bot+6}" font-family="{MONO}" font-size="15" fill="{col}">pinned {sha}</text>')
    # first version: accepted when added
    b.append(f'<line x1="360" y1="{top+46}" x2="360" y2="{bot-15}" stroke="{c["VIOLET"]}" stroke-opacity="0.8" stroke-width="2"/>')
    b.append(f'<text x="348" y="{mid+5}" font-family="{MONO}" font-size="15" fill="{c["MUTED"]}" text-anchor="end">ctxtra source add</text>')
    # reviewed and accepted
    gx = 640
    b.append(f'<line x1="{gx}" y1="{top+46}" x2="{gx}" y2="{bot-15}" stroke="{c["GREEN"]}" stroke-width="2"/>')
    b.append(f'<rect x="{gx-150}" y="{mid-17}" width="300" height="34" rx="8" fill="{c["PANEL"]}" stroke="{c["GREEN"]}"/>')
    b.append(f'<text x="{gx}" y="{mid+5}" font-family="{MONO}" font-size="15" fill="{c["TEXT"]}" text-anchor="middle">source update: diff → <tspan fill="{c["GREEN"]}" font-weight="700">Accept? y</tspan></text>')
    # reviewed and declined
    gx = 980
    b.append(f'<line x1="{gx}" y1="{top+46}" x2="{gx}" y2="{mid-17}" stroke="{c["RED"]}" stroke-width="2" stroke-dasharray="4 4"/>')
    b.append(f'<rect x="{gx-170}" y="{mid-17}" width="380" height="34" rx="8" fill="{c["PANEL"]}" stroke="{c["RED"]}"/>')
    b.append(f'<text x="{gx+20}" y="{mid+5}" font-family="{MONO}" font-size="15" fill="{c["TEXT"]}" text-anchor="middle">'
             f'<tspan fill="{c["AMBER"]}" font-weight="700">WARNING</tspan> scope widened → <tspan fill="{c["RED"]}" font-weight="700">N</tspan>, keep def5678</text>')
    b.append(f'<text x="56" y="{h-36}" font-size="17" fill="{c["MUTED"]}">ctxtra keeps its own copy, so you need no clone. Sessions never fetch. '
             f'<tspan font-family="{MONO}" fill="{c["TEXT"]}">ctxtra source restore</tspan> gives a new laptop exactly the accepted commits.</text>')
    write(f"team-sync-{theme}.svg", "\n".join(b), w, h,
          "A team repository is pinned to a commit; new commits reach your sessions only after you accept them in ctxtra source update")


# ---------------------------------------------------------------- the story: bindings, an edit, a team update

# Agent and GitHub marks, 24x24, from @lobehub/icons-static-svg 1.95.1 (Claude and Cursor from 1.70.0)
ICONS = {
    "claude": ['M4.709 15.955l4.72-2.647.08-.23-.08-.128H9.2l-.79-.048-2.698-.073-2.339-.097-2.266-.122-.571-.121L0 11.784l.055-.352.48-.321.686.06 1.52.103 2.278.158 1.652.097 2.449.255h.389l.055-.157-.134-.098-.103-.097-2.358-1.596-2.552-1.688-1.336-.972-.724-.491-.364-.462-.158-1.008.656-.722.881.06.225.061.893.686 1.908 1.476 2.491 1.833.365.304.145-.103.019-.073-.164-.274-1.355-2.446-1.446-2.49-.644-1.032-.17-.619a2.97 2.97 0 01-.104-.729L6.283.134 6.696 0l.996.134.42.364.62 1.414 1.002 2.229 1.555 3.03.456.898.243.832.091.255h.158V9.01l.128-1.706.237-2.095.23-2.695.08-.76.376-.91.747-.492.584.28.48.685-.067.444-.286 1.851-.559 2.903-.364 1.942h.212l.243-.242.985-1.306 1.652-2.064.73-.82.85-.904.547-.431h1.033l.76 1.129-.34 1.166-1.064 1.347-.881 1.142-1.264 1.7-.79 1.36.073.11.188-.02 2.856-.606 1.543-.28 1.841-.315.833.388.091.395-.328.807-1.969.486-2.309.462-3.439.813-.042.03.049.061 1.549.146.662.036h1.622l3.02.225.79.522.474.638-.079.485-1.215.62-1.64-.389-3.829-.91-1.312-.329h-.182v.11l1.093 1.068 2.006 1.81 2.509 2.33.127.578-.322.455-.34-.049-2.205-1.657-.851-.747-1.926-1.62h-.128v.17l.444.649 2.345 3.521.122 1.08-.17.353-.608.213-.668-.122-1.374-1.925-1.415-2.167-1.143-1.943-.14.08-.674 7.254-.316.37-.729.28-.607-.461-.322-.747.322-1.476.389-1.924.315-1.53.286-1.9.17-.632-.012-.042-.14.018-1.434 1.967-2.18 2.945-1.726 1.845-.414.164-.717-.37.067-.662.401-.589 2.388-3.036 1.44-1.882.93-1.086-.006-.158h-.055L4.132 18.56l-1.13.146-.487-.456.061-.746.231-.243 1.908-1.312-.006.006z'],
    "codex": ['M8.086.457a6.105 6.105 0 013.046-.415c1.333.153 2.521.72 3.564 1.7a.117.117 0 00.107.029c1.408-.346 2.762-.224 4.061.366l.063.03.154.076c1.357.703 2.33 1.77 2.918 3.198.278.679.418 1.388.421 2.126a5.655 5.655 0 01-.18 1.631.167.167 0 00.04.155 5.982 5.982 0 011.578 2.891c.385 1.901-.01 3.615-1.183 5.14l-.182.22a6.063 6.063 0 01-2.934 1.851.162.162 0 00-.108.102c-.255.736-.511 1.364-.987 1.992-1.199 1.582-2.962 2.462-4.948 2.451-1.583-.008-2.986-.587-4.21-1.736a.145.145 0 00-.14-.032c-.518.167-1.04.191-1.604.185a5.924 5.924 0 01-2.595-.622 6.058 6.058 0 01-2.146-1.781c-.203-.269-.404-.522-.551-.821a7.74 7.74 0 01-.495-1.283 6.11 6.11 0 01-.017-3.064.166.166 0 00.008-.074.115.115 0 00-.037-.064 5.958 5.958 0 01-1.38-2.202 5.196 5.196 0 01-.333-1.589 6.915 6.915 0 01.188-2.132c.45-1.484 1.309-2.648 2.577-3.493.282-.188.55-.334.802-.438.286-.12.573-.22.861-.304a.129.129 0 00.087-.087A6.016 6.016 0 015.635 2.31C6.315 1.464 7.132.846 8.086.457zm-.804 7.85a.848.848 0 00-1.473.842l1.694 2.965-1.688 2.848a.849.849 0 001.46.864l1.94-3.272a.849.849 0 00.007-.854l-1.94-3.393zm5.446 6.24a.849.849 0 000 1.695h4.848a.849.849 0 000-1.696h-4.848z'],
    "pi": ['M1 1h16.5v11H12v5.5H6.5V23H1V1zm5.5 5.5V12H12V6.5H6.5z', 'M17.5 12H23v11h-5.5V12z'],
    "opencode": ['M16 6H8v12h8V6zm4 16H4V2h16v20z'],
    "cursor": ['M22.106 5.68L12.5.135a.998.998 0 00-.998 0L1.893 5.68a.84.84 0 00-.419.726v11.186c0 .3.16.577.42.727l9.607 5.547a.999.999 0 00.998 0l9.608-5.547a.84.84 0 00.42-.727V6.407a.84.84 0 00-.42-.726zm-.603 1.176L12.228 22.92c-.063.108-.228.064-.228-.061V12.34a.59.59 0 00-.295-.51l-9.11-5.26c-.107-.062-.063-.228.062-.228h18.55c.264 0 .428.286.296.514z'],
    "github": ['M12 0c6.63 0 12 5.276 12 11.79-.001 5.067-3.29 9.567-8.175 11.187-.6.118-.825-.25-.825-.56 0-.398.015-1.665.015-3.242 0-1.105-.375-1.813-.81-2.181 2.67-.295 5.475-1.297 5.475-5.822 0-1.297-.465-2.344-1.23-3.169.12-.295.54-1.503-.12-3.125 0 0-1.005-.324-3.3 1.209a11.32 11.32 0 00-3-.398c-1.02 0-2.04.133-3 .398-2.295-1.518-3.3-1.209-3.3-1.209-.66 1.622-.24 2.83-.12 3.125-.765.825-1.23 1.887-1.23 3.169 0 4.51 2.79 5.527 5.46 5.822-.345.294-.66.81-.765 1.577-.69.31-2.415.81-3.495-.973-.225-.354-.9-1.223-1.845-1.209-1.005.015-.405.56.015.781.51.28 1.095 1.327 1.23 1.666.24.663 1.02 1.93 4.035 1.385 0 .988.015 1.916.015 2.196 0 .31-.225.664-.825.56C3.303 21.374-.003 16.867 0 11.791 0 5.276 5.37 0 12 0z'],
}


class Path:
    """Straight and cubic segments, so dots can move at the same speed on every path."""

    def __init__(self, x, y):
        self.pts = [("M", (x, y))]

    def L(self, x, y):
        self.pts.append(("L", (x, y)))
        return self

    def C(self, a, b, e):
        self.pts.append(("C", a, b, e))
        return self

    def d(self):
        return " ".join(s[0] + " ".join(f"{x:g} {y:g}" for x, y in s[1:]) for s in self.pts)

    def length(self):
        n, cur = 0.0, self.pts[0][1]
        for s in self.pts[1:]:
            if s[0] == "L":
                n += ((s[1][0] - cur[0]) ** 2 + (s[1][1] - cur[1]) ** 2) ** 0.5
            else:
                p0, (a, b, e) = cur, s[1:]
                prev = p0
                for k in range(1, 21):
                    t = k / 20
                    q = [(1 - t) ** 3 * p0[i] + 3 * (1 - t) ** 2 * t * a[i] + 3 * (1 - t) * t ** 2 * b[i] + t ** 3 * e[i] for i in (0, 1)]
                    n += ((q[0] - prev[0]) ** 2 + (q[1] - prev[1]) ** 2) ** 0.5
                    prev = q
            cur = s[-1]
        return n


def story(theme):
    """ctxtra's hooks in every agent; then, one at a time: your files, a binding by remote, a team package,
    an edit that reaches every matching session, and a team update that waits for review."""
    c = THEMES[theme]
    W, H, T = 960, 600, 23.0
    t_personal, t_acme, t_team, t_edit, t_update, t_clean = 2.4, 5.0, 7.6, 10.6, 14.8, 19.4
    b = [frame(c, W, H, grid=True), gradient(c)]

    def anim(attr, pts):
        if pts[0][0] > 0:
            pts = [(0, pts[0][1])] + pts
        if pts[-1][0] < T:
            pts = pts + [(T, pts[-1][1])]
        return (f'<animate attributeName="{attr}" values="{";".join(str(v) for _, v in pts)}" '
                f'keyTimes="{";".join(f"{t/T:.4f}" for t, _ in pts)}" dur="{T}s" repeatCount="indefinite"/>')

    def show(a, z, body, f=0.4):
        """Fades body in at a and out at z; a of 0 means it is there from the start of the loop."""
        pts = [(a, 0), (a + f, 1)] if a > 0 else [(0, 1)]
        pts += [(z - f, 1), (z, 0)] if z < T else []
        return f'<g opacity="{0 if a > 0 else 1}">{anim("opacity", pts)}{body}</g>'

    def mono(x, y, text, size=14, col="TEXT", weight="400", anchor="start"):
        return (f'<text x="{x:g}" y="{y:g}" font-family="{MONO}" font-size="{size}" fill="{c[col]}" '
                f'font-weight="{weight}" text-anchor="{anchor}" xml:space="preserve">{escape(text)}</text>')

    def sans(x, y, text, size=13, col="MUTED"):
        return f'<text x="{x:g}" y="{y:g}" font-size="{size}" fill="{c[col]}" text-anchor="middle">{escape(text)}</text>'

    def icon(name, cx, cy, size=22):
        fill = "#D97757" if name == "claude" else c["TEXT"]
        paths = "".join(f'<path d="{d}"/>' for d in ICONS[name])
        return f'<g transform="translate({cx - size/2:g} {cy - size/2:g}) scale({size/24:g})" fill="{fill}" fill-rule="evenodd">{paths}</g>'

    def chip(x, y, text, col):
        return (f'<rect x="{x}" y="{y}" width="262" height="34" rx="9" fill="{c["BG"]}" stroke="{c["EDGE"]}"/>'
                f'<circle cx="{x+18}" cy="{y+17}" r="6" fill="{c[col]}"/>' + mono(x + 32, y + 22, text))

    def flow(p, col, a, phase=0.0, speed=110, gap=70):
        n_len = p.length()
        dur, n = n_len / speed, max(1, round(n_len / gap))
        s = ""
        for i in range(n):
            beg = f'begin="{-(i + phase) * dur / n:.2f}s" dur="{dur:.2f}s" repeatCount="indefinite"'
            s += (f'<circle r="4" fill="{c[col]}" opacity="0"><animateMotion path="{p.d()}" {beg}/>'
                  f'<animate attributeName="opacity" values="0;1;1;0" keyTimes="0;0.08;0.9;1" {beg}/></circle>')
        return show(a, T, s, 0.3)

    def pulse(p, col, a, dur=1.3):
        """One big dot that runs along p once, at a."""
        kt = f"0;{a/T:.4f};{(a+dur)/T:.4f};1"
        return (f'<circle r="8" fill="{c[col]}" opacity="0">'
                f'<animateMotion path="{p.d()}" keyPoints="0;0;1;1" keyTimes="{kt}" calcMode="linear" dur="{T}s" repeatCount="indefinite"/>'
                + anim("opacity", [(a, 0), (a + 0.05, 1), (a + dur, 1), (a + dur + 0.05, 0)]) + "</circle>")

    # ---- projects: the agents in each, with ctxtra's hook on top
    PX, PW, ST, SH = [40, 345, 650], 270, 420, 70
    projects = [("~/dev/acme/billing", None), ("~/wt/billing-fix", "worktree"), ("~/dev/side-project", None)]
    agents = [("claude", "Claude"), ("codex", "Codex"), ("opencode", "OpenCode"), ("pi", "Pi"), ("cursor", "Cursor")]
    hooks = [x + PW - 50 for x in PX]
    pills_y = ST - 11
    for x, (path, tag) in zip(PX, projects):
        s = mono(x + 2, ST - 20, path, 14, "TEXT", "700")
        if tag:
            s += mono(x + 12 + 8.4 * len(path), ST - 20, tag, 12, "CYAN")
        s += f'<rect x="{x}" y="{ST}" width="{PW}" height="{SH}" rx="14" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>'
        for k, (name, lb) in enumerate(agents + [(None, "more")]):
            ax = x + 26 + k * 44
            if name:
                s += icon(name, ax, ST + 30) + sans(ax, ST + 58, lb, 9.5 if len(lb) > 6 else 11)
            else:
                s += (f'<circle cx="{ax}" cy="{ST+30}" r="11" fill="none" stroke="{c["DIM"]}" stroke-dasharray="3 3"/>'
                      + sans(ax, ST + 35, "+", 15) + sans(ax, ST + 58, lb, 11))
        b.append(s)

    # ---- sources: your folder and a team repo
    b.append(show(t_personal, T,
                  f'<rect x="40" y="72" width="290" height="126" rx="12" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>'
                  + mono(56, 98, "~/agent-context/", 15, "TEXT", "700") + mono(314, 98, "yours · live", 12, "GREEN", anchor="end")
                  + chip(54, 110, "me.md", "VIOLET")))
    b.append(show(t_acme, T, chip(54, 150, "go-style.md", "CYAN")))
    b.append(show(t_team, T,
                  f'<rect x="630" y="72" width="290" height="126" rx="12" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>'
                  + icon("github", 656, 92) + mono(674, 98, "acme/team-ctx", 15, "TEXT", "700")
                  + chip(644, 110, "billing.md", "AMBER") + chip(644, 150, "deploy/ skill", "LIME")))

    # ---- bindings: a line per binding, from its files to the hooks of the projects it matches
    bindings = [  # x where it leaves the source, rail y, color, projects, selector, label x and anchor, start
        (120, 250, "VIOLET", [0, 1, 2], "global: true", 132, "start", t_personal),
        (300, 296, "CYAN", [0, 1], "repos: github.com/acme/*", 312, "start", t_acme),
        (775, 342, "AMBER", [0, 1], "repos: github.com/acme/billing  (the package's binding)", 763, "end", t_team),
    ]
    paths = {}
    for k, (sx, ry, col, matched, sel, lx, anchor, a) in enumerate(bindings):
        for j in matched:
            hx = hooks[j] + (k - 1) * 18
            p = paths[k, j] = Path(sx, 198).L(sx, ry).L(hx, ry).L(hx, pills_y)
            b.append(show(a, T, f'<path d="{p.d()}" fill="none" stroke="{c[col]}" stroke-opacity="{c["EDGE_OP"]}" stroke-width="2" '
                                f'pathLength="1" stroke-dasharray="1" stroke-dashoffset="1">'
                                + anim("stroke-dashoffset", [(a, 1), (a + 0.7, 0)]) + "</path>", 0.1))
            b.append(flow(p, col, a + 0.7, phase=0.3 * j))
            if k == 2:  # the package's binding carries the skill too
                b.append(flow(p, "LIME", a + 0.7, phase=0.3 * j + 0.5))
        tw = 7.8 * len(sel) + 16
        bx = lx - 8 if anchor == "start" else lx - tw + 8
        b.append(show(a + 0.5, T, f'<rect x="{bx:g}" y="{ry-24}" width="{tw:g}" height="20" rx="5" fill="{c["BG"]}"/>'
                      + mono(lx, ry - 9, sel, 13, "MUTED", anchor=anchor)))

    # ---- what each session gets, counted as ctxtra's summary counts it, and the repos' state
    for x, (files, skills) in zip(PX, [([0, 1, 2], 1), ([0, 1, 2], 1), ([0], 0)]):
        dots = "".join(f'<circle cx="{x + 8 + 16*n}" cy="{ST + SH + 22}" r="5.5" fill="{c[FILE_COLORS[i]]}"/>'
                       for n, i in enumerate(files + [3] * skills))
        n = len(files)
        text = f"{n} instruction{'s' if n != 1 else ''}" + (f", {skills} skill" if skills else "")
        b.append(show(t_team + 0.8, T, dots + mono(x + 12 + 16 * (n + skills), ST + SH + 27, text, 13, "MUTED")))
        b.append(show(t_clean, T, mono(x + 8, ST + SH + 50, "✓ git status: clean", 13, "GREEN")))

    # ---- edit a file once: every matching session gets it
    a = t_edit
    b.append(show(a, a + 3.4, f'<rect x="54" y="150" width="262" height="34" rx="9" fill="none" stroke="{c["AMBER"]}" stroke-width="2"/>'
                  + mono(312, 172, "✎ edited", 12, "AMBER", anchor="end"), 0.2))
    for j in (0, 1):
        b.append(pulse(paths[1, j], "CYAN", a + 0.5))
        b.append(show(a + 1.8, a + 3.6, f'<rect x="{PX[j]}" y="{ST}" width="{PW}" height="{SH}" rx="14" fill="none" stroke="{c["CYAN"]}" stroke-width="2"/>'
                      + mono(PX[j] + PW / 2, ST + SH + 50, "next session: new go-style.md", 12, "CYAN", anchor="middle"), 0.2))

    # ---- a team commit waits for review, then goes out
    a = t_update
    pin = lambda text: f'<rect x="800" y="85" width="106" height="18" fill="{c["PANEL"]}"/>' + mono(906, 98, text, 12, "CYAN", anchor="end")
    b.append(show(t_team, a + 2.2, pin("pinned a1b2c3d"), 0.2))
    b.append(show(a, a + 1.4, f'<rect x="586" y="206" width="334" height="24" rx="6" fill="{c["BG"]}"/>'
                  + mono(906, 223, "new commit e4f5a6b · waits for your review", 12, "AMBER", anchor="end"), 0.2))
    b.append(show(a + 1.4, a + 3.0, f'<rect x="660" y="205" width="246" height="26" rx="6" fill="{c["BG"]}" stroke="{c["EDGE"]}"/>'
                  + mono(672, 223, "$ ctxtra source update", 13) + mono(896, 223, "✓", 13, "GREEN", anchor="end"), 0.2))
    b.append(show(a + 2.2, T, pin("pinned e4f5a6b"), 0.2))
    for j in (0, 1):
        b.append(pulse(paths[2, j], "AMBER", a + 2.4))

    # the hooks go last, so that no line or highlight covers them
    for hx in hooks:
        b.append(f'<rect x="{hx-38}" y="{pills_y}" width="76" height="22" rx="11" fill="{c["BG"]}" stroke="url(#g)" stroke-width="2"/>'
                 + mono(hx, pills_y + 16, "ctxtra", 12, "TEXT", "700", "middle"))

    for text, a, z in [("ctxtra hooks into every agent you use.", 0, t_personal),
                       ("Your own instructions reach every session.", t_personal, t_acme),
                       ("Bind more by folder or Git remote. Worktrees match too.", t_acme, t_team),
                       ("Add your team's package. It brings its own bindings.", t_team, t_edit - 0.2),
                       ("Edit a file once. Every matching session gets it, in every agent.", t_edit - 0.2, t_update - 0.2),
                       ("Team files stay at the commit you accepted, until you take the update.", t_update - 0.2, t_clean - 0.2),
                       ("Nothing is written into your repos.", t_clean - 0.2, T)]:
        b.append(show(a, z, f'<text x="{W/2}" y="44" font-size="19" fill="{c["TEXT"]}" text-anchor="middle">{escape(text)}</text>', 0.3))

    write(f"story-{theme}.svg", "\n".join(b), W, H,
          "ctxtra hooks into every agent. Bindings add your own instruction files and a pinned team package to "
          "sessions by folder or Git remote, worktrees included. An edited file reaches every matching session, "
          "a team update waits for your review, and nothing is written into the repos")


for theme in THEMES:
    hero(theme)
    team_sync(theme)
    story(theme)
    skill_chat(theme)
demo()
social()
