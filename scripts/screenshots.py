#!/usr/bin/env python3
"""Regenerate the README screenshots in assets/.

Renders the locally built binary (./claude-statusline) against a synthetic
payload plus a fabricated hour of session history — so burn rates,
projections, and trends all light up — once per theme, then screenshots the
ANSI output via headless Chrome (real font fallback, so every glyph renders).

Usage:
    go build -o claude-statusline .
    python3 scripts/screenshots.py

Requires Google Chrome. Everything runs against a throwaway HOME under /tmp;
your real config and state are never touched.
"""
import json
import os
import re
import shutil
import subprocess
import time

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BIN = os.path.join(REPO, "claude-statusline")
ASSETS = os.path.join(REPO, "assets")
WORK = "/tmp/claude-statusline-screenshots"
CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

THEMES = ["classic", "catppuccin-mocha", "nord", "dracula", "gruvbox-dark", "tokyo-night", "newsprint"]

# Canonical background for each theme (the statusline inherits the
# terminal's background, so the screenshot supplies each palette's own).
BG = {
    "classic": "#1d1f21",
    "catppuccin-mocha": "#1e1e2e",
    "nord": "#2e3440",
    "dracula": "#282a36",
    "gruvbox-dark": "#282828",
    "tokyo-night": "#1a1b26",
    "newsprint": "#1a1815",
}

# 16-color ANSI palette used for the classic theme's screenshot (classic
# renders basic ANSI codes; a real terminal would supply these colors).
ANSI16 = {
    30: "#3a3d4d", 31: "#ff5c57", 32: "#5af78e", 33: "#f3f99d",
    34: "#57c7ff", 35: "#ff6ac1", 36: "#9aedfe", 37: "#f1f1f0",
    90: "#7b7f8b", 91: "#ff5c57", 92: "#5af78e", 93: "#f3f99d",
    94: "#57c7ff", 95: "#ff6ac1", 96: "#9aedfe", 97: "#f1f1f0",
}

DEFAULT_FG = "#dcdfe4"
COLUMNS = "120"  # reflow width: keeps lines 1-2 intact, splits the bars 2/2

SESSION_ID = "screenshot-demo"
now = int(time.time())

claude_payload = {
    "session_id": SESSION_ID,
    "session_name": "refactor-auth",
    "version": "2.1.90",
    "model": {"display_name": "Claude Sonnet 4.6", "id": "claude-sonnet-4-6"},
    "output_style": {"name": "Explanatory"},
    "workspace": {
        "current_dir": "/Users/me/code/my-project",
        "project_dir": "/Users/me/code/my-project",
        "git_worktree": "my-project",
    },
    "cost": {
        "total_cost_usd": 1.84,
        "total_lines_added": 128,
        "total_lines_removed": 45,
        "total_duration_ms": 3661000,
        "total_api_duration_ms": 2400000,
    },
    "context_window": {
        "total_input_tokens": 1234567,
        "total_output_tokens": 89012,
        "context_window_size": 200000,
        "used_percentage": 72.5,
        "current_usage": {
            "input_tokens": 1200000,
            "output_tokens": 89012,
            "cache_creation_input_tokens": 10000,
            "cache_read_input_tokens": 50000,
        },
    },
    "rate_limits": {
        "five_hour": {"used_percentage": 45, "resets_at": now + 2 * 3600 + 30 * 60},
        "seven_day": {"used_percentage": 12, "resets_at": now + 3 * 86400 + 4 * 3600},
    },
    "worktree": {"name": "my-project", "branch": "feature/sparkle"},
    "effort": {"level": "high"},
}

agy_payload = {
    "conversation_id": "fbce29fe-0688-4fba-8cc1-0b769834c6d7",
    "product": "antigravity",
    "version": "1.0.2",
    "model": {"display_name": "Gemini 3.5 Flash (High)"},
    "workspace": {
        "current_dir": "/Users/me/code/my-project",
        "project_dir": "file:///Users/me/code/my-project",
    },
    "context_window": {
        "total_input_tokens": 116778,
        "total_output_tokens": 35463,
        "context_window_size": 1048576,
        "used_percentage": 11.1,
    },
    "agent_state": "tool_use",
    "sandbox": {"enabled": False},
    "artifact_count": 2,
    "plan_tier": "Google AI Pro",
}


def build_state():
    """One hour of rising history ending at the payload's current values."""
    samples = []
    n = 13  # every 5 minutes for an hour
    for i in range(n):
        f = i / (n - 1)
        samples.append({
            "t": now - 3600 + int(f * 3600),
            "cost": round(0.40 + f * (1.84 - 0.40), 4),
            "ctx": round(38.0 + f * (72.5 - 38.0), 2),
            "in": int(400000 + f * (1234567 - 400000)),
            "out": int(30000 + f * (89012 - 30000)),
            "rl5h": round(31.0 + f * (45.0 - 31.0), 2),
            "rl7d": round(10.5 + f * (12.0 - 10.5), 2),
        })
    return {"session_id": SESSION_ID, "samples": samples}


def render(theme, payload_obj, with_state):
    home = os.path.join(WORK, "home")
    shutil.rmtree(home, ignore_errors=True)
    cfg_dir = os.path.join(home, ".config", "claude-statusline")
    os.makedirs(cfg_dir)
    with open(os.path.join(cfg_dir, "config.toml"), "w") as f:
        f.write(f'theme = "{theme}"\n')
    if with_state:
        sess_dir = os.path.join(home, ".local", "state", "claude-statusline", "sessions")
        os.makedirs(sess_dir)
        with open(os.path.join(sess_dir, SESSION_ID + ".json"), "w") as f:
            json.dump(build_state(), f)
    env = dict(os.environ)
    env.update({
        "HOME": home,
        "COLORTERM": "truecolor",
        "TERM": "xterm-256color",
        "COLUMNS": COLUMNS,
    })
    for k in ("NO_COLOR", "XDG_STATE_HOME", "XDG_CONFIG_HOME"):
        env.pop(k, None)
    p = subprocess.run(
        [BIN], input=json.dumps(payload_obj), capture_output=True, text=True, env=env
    )
    if p.returncode != 0 or not p.stdout.strip():
        raise SystemExit(f"{theme}: render failed: rc={p.returncode} stderr={p.stderr}")
    return p.stdout


# ─── ANSI → HTML ─────────────────────────────────────────────────────

SGR_RE = re.compile(r"\x1b\[([0-9;]*)m")


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def ansi_line_to_html(line):
    out = []
    fg = None
    pos = 0
    for m in SGR_RE.finditer(line):
        if m.start() > pos:
            text = esc(line[pos:m.start()])
            out.append(f'<span style="color:{fg}">{text}</span>' if fg else text)
        pos = m.end()
        params = [int(p) for p in m.group(1).split(";") if p != ""] or [0]
        i = 0
        while i < len(params):
            p = params[i]
            if p == 0:
                fg = None
            elif p == 38 and i + 4 <= len(params) and params[i + 1] == 2:
                r, g, b = params[i + 2], params[i + 3], params[i + 4]
                fg = f"#{r:02x}{g:02x}{b:02x}"
                i += 4
            elif p in ANSI16:
                fg = ANSI16[p]
            i += 1
    if pos < len(line):
        text = esc(line[pos:])
        out.append(f'<span style="color:{fg}">{text}</span>' if fg else text)
    return "".join(out)


# ─── HTML window → PNG ───────────────────────────────────────────────
# Fixed geometry (CSS px) so the capture window size is exact.

MARGIN = 28          # transparent margin around the window (room for shadow)
TITLEBAR = 34
PRE_PAD_TOP = 4
PRE_PAD_BOTTOM = 18
LINE_HEIGHT = 23
WINDOW_WIDTH = 950

PAGE = """<!doctype html>
<meta charset="utf-8">
<style>
  * {{ margin: 0; padding: 0; box-sizing: border-box; }}
  body {{ background: transparent; }}
  .wrap {{ padding: {margin}px; width: {page_w}px; }}
  .window {{
    width: {win_w}px;
    background: {bg};
    border-radius: 11px;
    box-shadow: 0 14px 34px rgba(0,0,0,0.40), 0 3px 10px rgba(0,0,0,0.28);
  }}
  .titlebar {{
    height: {titlebar}px;
    display: flex; align-items: center; gap: 8px;
    padding-left: 15px;
  }}
  .dot {{ width: 12px; height: 12px; border-radius: 50%; }}
  pre {{
    padding: {pre_top}px 22px {pre_bottom}px 16px;
    font: 14px/{lh}px "SF Mono", Menlo, Monaco, monospace;
    color: {fg};
    white-space: pre;
  }}
</style>
<div class="wrap">
  <div class="window">
    <div class="titlebar">
      <div class="dot" style="background:#ff5f57"></div>
      <div class="dot" style="background:#febc2e"></div>
      <div class="dot" style="background:#28c840"></div>
    </div>
    <pre>{content}</pre>
  </div>
</div>
"""


def screenshot(name, ansi, bg):
    lines = ansi.rstrip("\n").split("\n")
    content = "\n".join(ansi_line_to_html(l) for l in lines)
    page_w = WINDOW_WIDTH + 2 * MARGIN
    page_h = (2 * MARGIN + TITLEBAR + PRE_PAD_TOP + PRE_PAD_BOTTOM
              + LINE_HEIGHT * len(lines))
    html_path = os.path.join(WORK, name + ".html")
    with open(html_path, "w") as f:
        f.write(PAGE.format(
            margin=MARGIN, page_w=page_w, win_w=WINDOW_WIDTH, bg=bg,
            titlebar=TITLEBAR, pre_top=PRE_PAD_TOP, pre_bottom=PRE_PAD_BOTTOM,
            lh=LINE_HEIGHT, fg=DEFAULT_FG, content=content,
        ))
    png_path = os.path.join(ASSETS, name + ".png")
    subprocess.run([
        CHROME, "--headless=new",
        f"--screenshot={png_path}",
        f"--window-size={page_w},{page_h}",
        "--force-device-scale-factor=2",
        "--default-background-color=00000000",
        "--hide-scrollbars", "--disable-gpu",
        f"file://{html_path}",
    ], check=True, capture_output=True)
    print("wrote", os.path.relpath(png_path, REPO))


def main():
    if not os.path.exists(BIN):
        raise SystemExit("build first: go build -o claude-statusline .")
    shutil.rmtree(WORK, ignore_errors=True)
    os.makedirs(WORK)
    os.makedirs(ASSETS, exist_ok=True)
    for theme in THEMES:
        screenshot(f"claude-{theme}", render(theme, claude_payload, True), BG[theme])
    screenshot("agy-classic", render("classic", agy_payload, False), BG["classic"])


if __name__ == "__main__":
    main()
