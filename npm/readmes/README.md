# @morgan.rebrand/claude-statusline

A fast, themeable statusline for [Claude Code](https://claude.ai/code), [Antigravity](https://antigravity.google/product/antigravity-cli) (`agy`), and [Pi](https://pi.dev) — your session's cost, context, and limits at a glance.

This npm package installs a small Node shim that selects and spawns the correct prebuilt Go binary for your platform via `optionalDependencies`. It is published automatically with each GitHub release.

## Install

```bash
npm i -g @morgan.rebrand/claude-statusline
claude-statusline install
```

Requires Node 14+.

> Homebrew and manual installs are lower latency because they avoid the Node spawn overhead on every render. npm is convenient when you already manage tools with npm or when installing inside Pi.

## Usage

Once installed, the `claude-statusline` command reads a JSON payload from stdin and prints the rendered statusline:

```bash
echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | claude-statusline
```

Run `claude-statusline install` to wire the renderer into Claude Code, Antigravity, or Pi and verify it works.

## Pi

Install as a Pi extension:

```bash
pi install npm:@morgan.rebrand/claude-statusline
```

No separate `claude-statusline install` step is needed inside Pi. The extension wires the renderer into Pi's footer and refreshes on session/turn/model events.

Upgrade with Pi's package manager:

```bash
pi update --extension npm:@morgan.rebrand/claude-statusline
```

## Configure

Run the interactive TUI:

```bash
claude-statusline configure
```

Configuration lives at `~/.config/claude-statusline/config.toml`.

## Documentation

- Full README and feature list: <https://github.com/callmemorgan/claude-statusline#readme>
- Releases: <https://github.com/callmemorgan/claude-statusline/releases>
- Issues: <https://github.com/callmemorgan/claude-statusline/issues>

## License

MIT
