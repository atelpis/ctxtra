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


# ---------------------------------------------------------------- what each approach gives you

def compare(theme):
    """Three columns: an agent out of the box, generator tools, ctxtra."""
    c = THEMES[theme]
    w, h = 1280, 640
    b = [frame(c, w, h), gradient(c)]
    b.append(f'<text x="40" y="58" font-size="15" font-weight="700" letter-spacing="2" fill="{c["CYAN"]}">WHERE YOUR AGENT INSTRUCTIONS LIVE</text>')
    b.append(f'<text x="40" y="92" font-size="25" font-weight="600" fill="{c["TEXT"]}">Out of the box, with a generator, and with ctxtra</text>')
    cols = [
        ("Agent out of the box", "each agent reads its own files", [
            ("~/.claude/CLAUDE.md", "every repo", "DIM"),
            ("~/.codex/AGENTS.md", "every repo", "DIM"),
            ("~/dev/AGENTS.md", "parent folder", "DIM"),
            ("~/dev/shop-api/", "", None),
            ("  AGENTS.md", "in the repo", "AMBER"),
            ("  api/AGENTS.md", "in the repo", "AMBER"),
        ], [
            (False, "Different files and places per agent"),
            (False, "Personal notes: global, or in the project"),
            (False, "Scoped by folder, not by repo identity"),
        ]),
        ("Generator tools", "one source, generated into each agent's files", [
            ("~/dev/shop-api/", "", None),
            ("  .ruler/  .rulesync/", "source", "DIM"),
            ("  AGENTS.md", "generated", "AMBER"),
            ("  CLAUDE.md", "generated", "AMBER"),
            ("  .cursor/rules/", "generated", "AMBER"),
            ("  .gitignore", "+ generated files", "AMBER"),
            ("~/.claude/  ~/.codex/", "global mode", "DIM"),
        ], [
            (True, "One source for many agents"),
            (False, "Writes generated files into projects"),
            (False, "Regenerate after each change, or watch"),
        ]),
        ("ctxtra", "injected into the session at runtime", [
            ("~/dev/shop-api/", "nothing added", "GREEN"),
            ("  AGENTS.md", "the team's, untouched", "DIM"),
            ("~/dotfiles/", "your package, live", "GREEN"),
            ("  go-style.md  review/", "", None),
            ("your-org/team-ctx", "shared, pinned", "CYAN"),
            ("  services.md  deploy/", "", None),
        ], [
            (True, "All supported agents, one config"),
            (True, "Nothing written into your projects"),
            (True, "Local files, or shared repos you review"),
            (True, "Target by folder, git remote or subpath"),
            (True, "Every worktree, automatically"),
        ]),
    ]
    cw, gap, x0, y0, ch = 384, 24, 40, 122, 486
    for k, (title, sub, tree, verdicts) in enumerate(cols):
        x = x0 + k * (cw + gap)
        ours = k == 2
        b.append(f'<rect x="{x}" y="{y0}" width="{cw}" height="{ch}" rx="14" fill="{c["PANEL"]}" '
                 f'stroke="{"url(#g)" if ours else c["EDGE"]}" stroke-width="{2.5 if ours else 1}"/>')
        b.append(f'<text x="{x+22}" y="{y0+40}" font-size="21" font-weight="700" fill="{c["TEXT"]}"'
                 f'{f" font-family={chr(34)}{MONO}{chr(34)}" if ours else ""}>{escape(title)}</text>')
        b.append(f'<text x="{x+22}" y="{y0+64}" font-size="15" fill="{c["MUTED"]}">{escape(sub)}</text>')
        for n, (path, tag, col) in enumerate(tree):
            y = y0 + 104 + n * 28
            b.append(f'<text x="{x+22}" y="{y}" font-family="{MONO}" font-size="15" fill="{c["TEXT"]}" xml:space="preserve">{escape(path)}</text>')
            if tag:
                b.append(f'<text x="{x+cw-22}" y="{y}" font-size="14" fill="{c[col]}" text-anchor="end">{escape(tag)}</text>')
        ly = y0 + 104 + 7 * 28 - 6
        b.append(f'<line x1="{x+22}" y1="{ly}" x2="{x+cw-22}" y2="{ly}" stroke="{c["EDGE"]}"/>')
        for n, (good, text) in enumerate(verdicts):
            y = ly + 34 + n * 30
            mark, mc = ("✓", c["GREEN"]) if good else ("✕", c["RED"])
            b.append(f'<text x="{x+22}" y="{y}" font-size="17" font-weight="700" fill="{mc}">{mark}</text>')
            b.append(f'<text x="{x+46}" y="{y}" font-size="16" fill="{c["TEXT"]}">{escape(text)}</text>')
    write(f"compare-{theme}.svg", "\n".join(b), w, h,
          "Where agent instructions live: out of the box each agent reads its own files, global or committed; "
          "generator tools write generated files into every project; ctxtra injects them at runtime and writes nothing into your projects")


for theme in THEMES:
    hero(theme)
    team_sync(theme)
    compare(theme)
demo()
social()
