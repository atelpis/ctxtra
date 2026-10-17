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
    ("cursor", "~/Downloads", [0]),
]


# Packages on the left, sessions on the right; every edge is a binding.
PACKAGES = [
    ("personal", "~/dev/dotfiles", "live", [0, 1]),
    ("team", "acme/team-ctx", "pinned", [2, 3]),
]


def hero_body(c, w, h, top=0, animate=True, paths=True):
    """The hero content; top shifts it down for the taller social preview."""
    col = lambda i: c[FILE_COLORS[i]]
    b = [gradient(c)]
    t = top
    b.append(f'<text x="62" y="{t+140}" font-family="{MONO}" font-size="104" font-weight="700" letter-spacing="-3">'
             f'<tspan fill="url(#g)">ctx</tspan><tspan fill="{c["TEXT"]}">tra</tspan></text>')
    caret = f'<rect x="438" y="{t+66}" width="14" height="80" fill="{c["CYAN"]}">'
    if animate:
        caret += '<animate attributeName="opacity" values="1;1;0;0" keyTimes="0;0.5;0.5;1" dur="1.1s" repeatCount="indefinite"/>'
    b.append(caret + "</rect>")
    b.append(f'<text x="64" y="{t+212}" font-size="38" font-weight="700" fill="{c["TEXT"]}">Context for coding agents,</text>')
    b.append(f'<text x="64" y="{t+258}" font-size="38" font-weight="700" fill="{c["TEXT"]}">keyed by git repo.</text>')
    b.append(f'<text x="64" y="{t+304}" font-size="19" fill="{c["MUTED"]}">Nothing written into your repos.</text>')
    b.append(f'<text x="64" y="{t+331}" font-size="19" fill="{c["MUTED"]}">Nothing to set up per worktree.</text>')
    x = 64
    for name in ["Claude Code", "Codex", "Pi", "OpenCode", "Cursor CLI"]:
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
          "ctxtra: context for coding agents, keyed by git repo. Nothing written into your repos, nothing to set up per worktree")


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
    c = THEMES["dark"]
    w, h = 1100, 486
    T = 18.0  # loop length, seconds
    cw = 10.2  # monospace advance at 17px
    x0, lh = 34, 30
    scenes = [
        ("~/dev/acme/billing", "claude", [0, 1, 2], True, "# remote is github.com/acme/billing"),
        ("~/wt/billing-fix", "codex", [0, 1, 2], True, "# a worktree of it, outside ~/dev"),
        ("~/dev/side-project", "pi", [0, 1], False, "# under ~/dev, not acme/billing"),
        ("~/Downloads", "claude", [0], False, "# global only"),
    ]
    b = [frame(c, w, h, 14)]
    b.append(f'<rect x="1" y="1" width="{w-2}" height="44" rx="13" fill="{c["PANEL"]}"/>')
    b.append(f'<rect x="1" y="30" width="{w-2}" height="15" fill="{c["PANEL"]}"/>')
    b.append(f'<line x1="1" y1="45" x2="{w-1}" y2="45" stroke="{c["EDGE"]}"/>')
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

    y = 88
    start = 0.6
    for s, (cwd, agent, files, skill, note) in enumerate(scenes):
        cmd = f"cd {cwd} && {agent}"
        t0, t1 = start, start + 0.045 * len(cmd)
        t_out = t1 + 0.45
        full = cw * (len(cmd) + 2) + 6
        # the command, revealed by a growing clip
        b.append(f'<clipPath id="c{s}"><rect x="{x0}" y="{y-22}" height="{lh}" width="{full}">'
                 + anim("width", [0, t0, t1, T], ["0", "0", f"{full}", f"{full}"]) + "</rect></clipPath>")
        b.append(f'<g clip-path="url(#c{s})">'
                 f'<text x="{x0}" y="{y}" font-family="{MONO}" font-size="17" xml:space="preserve">'
                 f'<tspan fill="{c["GREEN"]}">❯</tspan> <tspan fill="{c["TEXT"]}">cd </tspan><tspan fill="{c["CYAN"]}">{cwd}</tspan>'
                 f'<tspan fill="{c["TEXT"]}"> &amp;&amp; </tspan><tspan fill="{c["TEXT"]}" font-weight="700">{agent}</tspan></text></g>')
        b.append(f'<text x="{x0 + cw*44}" y="{y}" font-family="{MONO}" font-size="16" fill="{c["DIM"]}">{escape(note)}{appear(t_out)}</text>')
        # ctxtra's summary line, each name in its color
        m = c["MUTED"]
        parts = [f'<tspan fill="{m}">ctxtra: added {len(files)} file{"s" if len(files) != 1 else ""} (</tspan>']
        for k, i in enumerate(files):
            if k:
                parts.append(f'<tspan fill="{m}">, </tspan>')
            parts.append(f'<tspan fill="{c[FILE_COLORS[i]]}">{FILE_NAMES[i]}</tspan>')
        parts.append(f'<tspan fill="{m}">)</tspan>')
        if skill:
            parts.append(f'<tspan fill="{m}"> and 1 skill (</tspan><tspan fill="{c["LIME"]}">deploy</tspan><tspan fill="{m}">)</tspan>')
        b.append(f'<text x="{x0 + cw*2}" y="{y+lh}" font-family="{MONO}" font-size="17" xml:space="preserve">'
                 + "".join(parts) + appear(t_out) + "</text>")
        y += lh * 2 + 26
        start = t_out + 2.2
    b.append(f'<text x="{x0}" y="{y}" font-family="{MONO}" font-size="17" fill="{c["GREEN"]}">❯{appear(start - 1.2)}</text>')
    b.append(f'<rect x="{x0 + cw*2}" y="{y-16}" width="10" height="20" fill="{c["TEXT"]}">{appear(start - 1.2, "0.8")}</rect>')
    write("demo.svg", "\n".join(b), w, h, "ctxtra demo: four sessions in four folders get four different sets of files from one config")


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
    b.append(f'<text x="374" y="{mid+5}" font-family="{MONO}" font-size="15" fill="{c["MUTED"]}">ctxtra add</text>')
    # reviewed and accepted
    gx = 640
    b.append(f'<line x1="{gx}" y1="{top+46}" x2="{gx}" y2="{bot-15}" stroke="{c["GREEN"]}" stroke-width="2"/>')
    b.append(f'<rect x="{gx-118}" y="{mid-17}" width="236" height="34" rx="8" fill="{c["PANEL"]}" stroke="{c["GREEN"]}"/>')
    b.append(f'<text x="{gx}" y="{mid+5}" font-family="{MONO}" font-size="15" fill="{c["TEXT"]}" text-anchor="middle">sync: diff → <tspan fill="{c["GREEN"]}" font-weight="700">Accept? y</tspan></text>')
    # reviewed and declined
    gx = 980
    b.append(f'<line x1="{gx}" y1="{top+46}" x2="{gx}" y2="{mid-17}" stroke="{c["RED"]}" stroke-width="2" stroke-dasharray="4 4"/>')
    b.append(f'<rect x="{gx-190}" y="{mid-17}" width="380" height="34" rx="8" fill="{c["PANEL"]}" stroke="{c["RED"]}"/>')
    b.append(f'<text x="{gx}" y="{mid+5}" font-family="{MONO}" font-size="15" fill="{c["TEXT"]}" text-anchor="middle">'
             f'<tspan fill="{c["AMBER"]}" font-weight="700">WARNING</tspan> scope widened → <tspan fill="{c["RED"]}" font-weight="700">N</tspan>, keep def5678</text>')
    b.append(f'<text x="56" y="{h-36}" font-size="17" fill="{c["MUTED"]}">ctxtra keeps its own copy, so you need no clone. Sessions never fetch. '
             f'<tspan font-family="{MONO}" fill="{c["TEXT"]}">ctxtra sync --restore</tspan> gives a new laptop exactly the accepted commits.</text>')
    write(f"team-sync-{theme}.svg", "\n".join(b), w, h,
          "A team repository is pinned to a commit; new commits reach your sessions only after you accept them in ctxtra sync")


# ---------------------------------------------------------------- the story: out of the box, generators, ctxtra

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
    """One loop, three slides: an agent out of the box, generator tools, ctxtra."""
    c = THEMES[theme]
    W, H, T = 880, 610, 46.0
    S2, S3 = 7.0, 20.0  # slide starts
    A, B, C, D = 172, 330, 440, 548  # rows: sources, projects, ctxtra or generate, agents
    b = [frame(c, W, H, grid=True), gradient(c)]

    def keys(pts):
        if pts[0][0] > 0:
            pts = [(0, pts[0][1])] + pts
        if pts[-1][0] < T:
            pts = pts + [(T, pts[-1][1])]
        return ";".join(str(v) for _, v in pts), ";".join(f"{t/T:.4f}" for t, _ in pts), len(pts) - 1

    def anim(attr, pts):
        v, k, _ = keys(pts)
        return f'<animate attributeName="{attr}" values="{v}" keyTimes="{k}" dur="{T}s" repeatCount="indefinite"/>'

    def move(pts):
        v, k, n = keys(pts)
        return (f'<animateTransform attributeName="transform" type="translate" values="{v}" keyTimes="{k}" '
                f'calcMode="spline" keySplines="{";".join(["0.3 0 0.2 1"] * n)}" dur="{T}s" repeatCount="indefinite"/>')

    def win(a, z, f=0.4):
        return anim("opacity", [(a, 0), (a + f, 1), (z - f, 1), (z, 0)])

    def show(a, z, body, f=0.4):
        return f'<g opacity="0">{win(a, z, f)}{body}</g>'

    def mono(x, y, text, size=17, col="TEXT", anchor="start", weight="400"):
        return (f'<text x="{x:g}" y="{y:g}" font-family="{MONO}" font-size="{size}" fill="{c[col]}" '
                f'text-anchor="{anchor}" font-weight="{weight}">{escape(text)}</text>')

    def box(cx, cy, w, text, col=None, h=44, stroke=None):
        x, y = cx - w / 2, cy - h / 2
        s = f'<rect x="{x:g}" y="{y:g}" width="{w}" height="{h}" rx="10" fill="{c["PANEL"]}" stroke="{c[stroke] if stroke else c["EDGE"]}"/>'
        if col:
            s += f'<circle cx="{x+20:g}" cy="{cy}" r="6" fill="{c[col]}"/>' + mono(x + 36, cy + 6, text)
        else:
            s += mono(x + 18, cy + 6, text, weight="700")
        return s

    def pill_w(name):
        return 30 + len(name) * 9.2

    def pill(cx, cy, name):
        w = pill_w(name)
        return (f'<rect x="{cx-w/2:g}" y="{cy-20}" width="{w:g}" height="40" rx="20" fill="{c["PANEL"]}" stroke="{c["EDGE"]}"/>'
                f'<text x="{cx:g}" y="{cy+6}" font-size="17" fill="{c["TEXT"]}" text-anchor="middle">{name}</text>')

    def line(p, a, z, draw=None, col="LINE", op=None):
        s = f'<path d="{p.d()}" fill="none" stroke="{c[col]}" stroke-width="2"'
        if col != "LINE":
            s += f' stroke-opacity="{c["EDGE_OP"]}"'
        if draw:
            s += ' pathLength="1" stroke-dasharray="1" stroke-dashoffset="1"'
        s += ">" + (anim("opacity", op) if op else win(a, z))
        if draw:
            s += anim("stroke-dashoffset", [(draw[0], 1), (draw[1], 0)])
        return s + "</path>"

    def flow(p, col, a, z, speed=150, gap=64, phase=0.0):
        n_len = p.length()
        dur, n = n_len / speed, max(1, round(n_len / gap))
        s = f'<g opacity="0">{win(a, z, 0.3)}'
        for i in range(n):
            beg = f'begin="{-(i + phase) * dur / n:.2f}s" dur="{dur:.2f}s" repeatCount="indefinite"'
            s += (f'<circle r="5" fill="{c[col]}" opacity="0"><animateMotion path="{p.d()}" {beg}/>'
                  f'<animate attributeName="opacity" values="0;1;1;0" keyTimes="0;0.06;0.9;1" {beg}/></circle>')
        return s + "</g>"

    def caption(text, a, z):
        return show(a, z, f'<text x="{W/2}" y="110" font-size="19" fill="{c["TEXT"]}" text-anchor="middle">{escape(text)}</text>', 0.3)

    # header: three tabs, the bar moves to the current one
    tabs = [(150, "1  Out of the box", 0, S2), (440, "2  Generators", S2, S3), (730, "3  ctxtra", S3, T)]
    for x, name, a, z in tabs:
        b.append(f'<text x="{x}" y="54" font-size="19" font-weight="700" fill="{c["DIM"]}" text-anchor="middle" xml:space="preserve">{name}</text>')
        b.append(show(a, z, f'<text x="{x}" y="54" font-size="19" font-weight="700" fill="{c["TEXT"]}" text-anchor="middle" xml:space="preserve">{name}</text>', 0.3))
    b.append(f'<g><rect x="-100" y="66" width="200" height="3" rx="1.5" fill="url(#g)"/>'
             + move([(0, "150 0"), (S2 - 0.3, "150 0"), (S2 + 0.3, "440 0"), (S3 - 0.3, "440 0"), (S3 + 0.3, "730 0"), (T - 0.3, "730 0"), (T, "150 0")])
             + "</g>")

    # ---- slide 1: one agent reads its own files
    z = S2 - 0.1
    b.append(line(Path(440, A + 22).L(440, B - 22), 0, z))
    b.append(line(Path(440, B + 22).L(440, D - 20), 0, z))
    b.append(flow(Path(440, A + 22).L(440, D - 20), "VIOLET", 0.6, z - 0.4))
    b.append(flow(Path(440, B + 22).L(440, D - 20), "AMBER", 1.0, z - 0.4))
    b.append(show(0, z, box(440, A, 300, "~/.claude/CLAUDE.md", "VIOLET") + box(440, B, 300, "my-proj/CLAUDE.md", "AMBER") + pill(440, D, "Claude Code")))
    b.append(caption("Out of the box: one agent reads its own files, in your home and in the repo.", 0, z))

    # ---- slide 2: a generator writes each agent's files into the project
    s, z = S2, S3 - 0.1
    gx, fx = 330, [132, 330, 528]
    files, agents = ["CLAUDE.md", "AGENTS.md", ".cursor/rules/"], ["Claude Code", "Codex", "Cursor"]
    b.append(line(Path(gx, A + 22).L(gx, 234), s, z))
    for i, x in enumerate(fx):
        b.append(line(Path(gx, 270).C((gx, 296), (x, 294), (x, 320)), s, z, draw=(s + 1.0, s + 1.4)))
        b.append(line(Path(x, 360).L(x, D - 20), s, z, draw=(s + 1.2 + 0.2 * i, s + 1.6 + 0.2 * i)))
        p = Path(gx, A + 22).L(gx, 270).C((gx, 296), (x, 294), (x, 320)).L(x, D - 20)
        b.append(flow(p, "VIOLET", s + 1.8, s + 5.4))
        b.append(flow(p, "VIOLET", s + 8.8, z - 0.4))
    b.append(show(s, z,
                  f'<rect x="28" y="298" width="604" height="84" rx="12" fill="none" stroke="{c["EDGE"]}"/>'
                  + mono(28, 290, "my-proj/", 15, "MUTED") + mono(632, 290, "written by the generator", 14, "AMBER", "end")
                  + box(gx, A, 220, "rules.md", "VIOLET") + mono(gx + 96, A + 5, "source", 13, "DIM", "end")
                  + f'<rect x="{gx-80}" y="234" width="160" height="36" rx="18" fill="{c["BG"]}" stroke="{c["LINE"]}"/>'
                  + mono(gx, 258, "$ generate", 16, "MUTED", "middle")
                  + "".join(pill(x, D, n) for x, n in zip(fx, agents))))
    # the generate button lights up on each run
    b.append(f'<rect x="{gx-80}" y="234" width="160" height="36" rx="18" fill="none" stroke="{c["CYAN"]}" stroke-width="2.5" opacity="0">'
             + anim("opacity", [(s + 0.6, 0), (s + 0.8, 1), (s + 1.4, 0), (s + 8.0, 0), (s + 8.2, 1), (s + 8.8, 0)]) + "</rect>")
    for i, (x, name) in enumerate(zip(fx, files)):
        b.append(show(s + 1.0 + 0.2 * i, z, box(x, 340, 188, name, "AMBER", 40), 0.3))
        b.append(show(s + 5.8, s + 8.6, f'<rect x="{x-94}" y="320" width="188" height="40" rx="10" fill="{c["BG"]}" fill-opacity="0.55" '
                      f'stroke="{c["RED"]}" stroke-width="2" stroke-dasharray="6 4"/>' + mono(x - 10, 376, "stale", 13, "RED", "end"), 0.3))
    b.append(show(s + 5.2, s + 8.6, f'<rect x="{gx-110}" y="{A-22}" width="220" height="44" rx="10" fill="none" stroke="{c["AMBER"]}" stroke-width="2"/>'
                  + mono(gx + 122, A + 6, "✎ edited", 15, "AMBER"), 0.3))
    b.append(show(s + 9.2, z, f'<rect x="652" y="298" width="200" height="84" rx="12" fill="none" stroke="{c["RED"]}" stroke-opacity="0.7" stroke-dasharray="6 4"/>'
                  + mono(652, 290, "new worktree/", 15, "MUTED")
                  + f'<text x="752" y="346" font-size="17" fill="{c["RED"]}" text-anchor="middle">✕ no files yet</text>'))
    b.append(caption("Generators: one source, written out as each agent's files in your project.", s, s + 5.2))
    b.append(caption("Change the source, and those files are stale until you run it again.", s + 5.2, s + 9.0))
    b.append(caption("A new clone or worktree gets nothing until you generate there too.", s + 9.0, z))

    # ---- slide 3: ctxtra
    s, z = S3, T - 0.1
    l = lambda t: s + t
    names = ["Claude Code", "Codex", "Pi", "OpenCode", "Cursor"]
    gap_ = 14
    total = sum(pill_w(n) for n in names) + gap_ * len(names) + 64
    x, slots = W / 2 - total / 2, []
    for n in names:
        slots.append(x + pill_w(n) / 2)
        x += pill_w(n) + gap_
    more_x = x + 4

    # the same start as slide 1
    dim = [(s, 0), (l(0.5), 1), (l(3.4), 1), (l(4.0), 0.3), (z - 0.4, 0.3), (z, 0)]
    b.append(line(Path(440, A + 22).L(440, B - 22), 0, 0, op=dim))
    b.append(line(Path(440, B + 22).L(440, C - 23), s, z))
    b.append(line(Path(440, C - 23).L(440, D - 20), s, l(4.4)))
    b.append(flow(Path(440, A + 22).L(440, D - 20), "VIOLET", l(0.6), l(3.4)))
    b.append(flow(Path(440, B + 22).L(440, D - 20), "AMBER", l(1.0), l(3.4)))
    b.append(f'<g opacity="0">{anim("opacity", dim)}{box(440, A, 300, "~/.claude/CLAUDE.md", "VIOLET")}</g>')

    # bindings: your folder into my-proj, a shared git repo into work-proj
    cyan = Path(150, A + 22).L(150, B).L(440, B).L(440, C - 23)
    lime = Path(730, A + 22).L(730, C).L(520, C)
    b.append(line(cyan, s, z, draw=(l(8.4), l(9.0)), col="CYAN"))
    b.append(line(Path(730, B + 22).L(730, C).L(520, C), l(13.0), z, draw=(l(13.4), l(13.9))))
    b.append(line(Path(730, A + 22).L(730, B - 22), s, z, draw=(l(15.6), l(16.0)), col="LIME"))
    fans = [Path(440, C + 23).C((440, 500), (x, 490), (x, D - 20)) for x in slots]
    for p in fans:
        b.append(line(p, l(5.0), z, draw=(l(5.0), l(5.6))))
    b.append(flow(cyan, "CYAN", l(9.0), z - 0.4))
    b.append(flow(lime, "LIME", l(16.2), z - 0.4))
    for p in fans:
        b.append(flow(p, "CYAN", l(9.6), z - 0.4, gap=90))
        b.append(flow(p, "LIME", l(16.8), z - 0.4, gap=90, phase=0.5))

    b.append(show(s, z, box(440, B, 300, "my-proj/CLAUDE.md", "AMBER")))
    b.append(show(l(10.2), z, mono(580, B + 5, "+0 files", 13, "GREEN", "end")))
    b.append(show(l(13.0), z, box(730, B, 240, "work-proj/")))
    b.append(show(l(17.4), z, mono(840, B + 5, "+0 files", 13, "GREEN", "end")))

    # ctxtra comes in, and the one agent becomes many
    b.append(show(l(3.2), z, f'<g>{move([(l(3.2), "0 -70"), (l(4.0), "0 0")])}'
                  f'<rect x="360" y="{C-23}" width="160" height="46" rx="12" fill="{c["BG"]}" stroke="url(#g)" stroke-width="2.5"/>'
                  f'<text x="440" y="{C+8}" font-family="{MONO}" font-size="22" font-weight="700" text-anchor="middle">'
                  f'<tspan fill="url(#g)">ctx</tspan><tspan fill="{c["TEXT"]}">tra</tspan></text></g>'))
    for k, (n, x) in enumerate(zip(names, slots)):
        if k == 0:
            b.append(show(s, z, f'<g>{move([(l(4.2), "0 0"), (l(5.2), f"{x-440:g} 0")])}{pill(440, D, n)}</g>'))
        else:
            b.append(show(l(4.5), z, f'<g>{move([(l(4.2), f"{440-x:g} 0"), (l(5.2), "0 0")])}{pill(x, D, n)}</g>', 0.6))
    b.append(show(l(5.0), z, f'<text x="{more_x:g}" y="{D+6}" font-size="17" fill="{c["MUTED"]}">+ more</text>'))

    b.append(show(l(6.6), z, mono(45, A - 32, "~/notes/", 15, "MUTED") + box(150, A, 210, "go-style.md", "CYAN")))
    b.append(show(l(13.8), z, mono(610, A - 32, "team repo (git), pinned", 15, "MUTED") + box(730, A, 240, "team.md", "LIME")))

    def binding(cx, target, col, a, dx):
        return show(a, z, f'<g>{move([(a, f"{dx} 0"), (a + 1.0, "0 0")])}'
                    f'<rect x="{cx-105}" y="226" width="210" height="52" rx="10" fill="{c["BG"]}" stroke="{c[col]}" stroke-width="2"/>'
                    + mono(cx - 90, 246, "binding", 13, "DIM")
                    + mono(cx - 90, 268, f"→ {target}", 16) + "</g>", 0.5)
    b.append(binding(150, "my-proj", "CYAN", l(7.4), -260))
    b.append(binding(730, "work-proj", "LIME", l(14.6), 260))

    b.append(caption("ctxtra: start from the same setup.", s, l(3.4)))
    b.append(caption("Add ctxtra once. It hooks into every supported agent.", l(3.4), l(6.6)))
    b.append(caption("Keep your own Markdown files in your own folder.", l(6.6), l(9.6)))
    b.append(caption("A binding sends them to my-proj, for every agent and every worktree.", l(9.6), l(13.0)))
    b.append(caption("Another project? Bind files from a shared git repo.", l(13.0), l(16.4)))
    b.append(caption("It stays at the commit you accepted. Updates wait for your review.", l(16.4), l(20.6)))
    b.append(caption("Two projects, two sources, every agent. Nothing written into either project.", l(20.6), z))

    write(f"story-{theme}.svg", "\n".join(b), W, H,
          "Out of the box, one agent reads its own files. Generators write each agent's files into your project and "
          "need a rerun after every change and in every new worktree. ctxtra hooks into every agent and adds files "
          "from your own folder or a pinned team repo, bound to each project, with nothing written into the projects")


for theme in THEMES:
    hero(theme)
    team_sync(theme)
    story(theme)
demo()
social()
