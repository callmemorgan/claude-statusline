# claude-statusline

A fast, dependency-free statusline renderer for [Claude Code](https://claude.ai/code).

Claude Code pipes a JSON payload to this binary on every turn. It renders a three-line colored summary:


## Features

- **Model & session** — display name, version, effort level badge
- **Workspace** — current directory, project context, git branch, worktree name
- **Cost & duration** — total spend, elapsed time, API efficiency percentage
- **Tokens** — compact formatting (1.2k, 3.4M) for input/output totals
- **Context window** — ASCII progress bar with percentage and >200k warning
- **Rate limits** — 5-hour and 7-day quota bars with countdown timers
- **Extras** — vim mode indicator, agent name, lines added/removed

## Install

```bash
go install github.com/callmemorgan/claude-statusline@latest
```

Or clone and build:

```bash
git clone https://github.com/callmemorgan/claude-statusline.git
cd claude-statusline
go build -o claude-statusline main.go
```

## Usage

```bash
# With Claude Code's JSON payload
echo '{"model":{"display_name":"Claude"},"workspace":{"current_dir":"~"}}' | ./claude-statusline

# Or configure Claude Code to use it as your statusline
claude config set statusline.command "/path/to/claude-statusline"
```

## Configuration

Segments are controlled by an **ordered array** — both visibility and display order:

```bash
claude-statusline --configure
```

Or edit `~/.config/claude-statusline/config.json` directly:

```json
{
  "segments": [
    "model",
    "directory",
    "git-branch",
    "cost",
    "context-window",
    "rate-limits"
  ]
}
```

- Segments render on their natural line (line 1 = workspace meta, line 2 = model/duration, line 3 = progress bars).
- Missing config = all segments in default order.
- Empty array `[]` = hide the statusline entirely.

## Why Go?

- **Zero dependencies** — standard library only
- **Fast** — parses JSON and renders in <1ms
- **Portable** — single static binary

## License

MIT
