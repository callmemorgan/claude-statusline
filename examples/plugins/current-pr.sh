#!/usr/bin/env bash
# Detects the current pull request for the repository at $STATUSLINE_DIR.
# Designed as an async claude-statusline plugin: it caches results and
# invalidates that cache when the working directory changes. Because the
# binary's async cache is keyed by command, cwd changes are reflected at the
# next refresh boundary (refresh_ms) -- this script clears its own cache
# immediately so the refresh fetches the correct value.
#
# Configurable TTL via STATUSLINE_PR_TTL (seconds, default 60).
# Example config.toml:
#
#   [[plugins]]
#   id = "current-pr"
#   command = "~/.config/claude-statusline/plugins/current-pr.sh"
#   async = true
#   refresh_ms = 10000
#   timeout_ms = 8000
#
# Requires `gh` (GitHub CLI) for PR detection. Falls back to a short branch
# indicator when `gh` is unavailable or no PR exists.

set -euo pipefail

TTL="${STATUSLINE_PR_TTL:-60}"
DIR="${STATUSLINE_DIR:-$PWD}"
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/claude-statusline/plugins/current-pr"
STATE_FILE="$STATE_DIR/state"

mkdir -p "$STATE_DIR"

now=$(date +%s)

# Fetch the current PR label using gh, or fall back to branch info.
fetch_label() {
	local branch pr
	if command -v gh >/dev/null 2>&1; then
		if pr=$(cd "$DIR" && gh pr view --json number,title --jq '"#" + (.number | tostring) + " " + .title' 2>/dev/null); then
			printf '%s' "$pr"
			return 0
		fi
	fi

	# Fallback: short branch name when there is no PR.
	if branch=$(cd "$DIR" && git symbolic-ref --short HEAD 2>/dev/null); then
		printf '%s' "$branch"
		return 0
	fi

	return 1
}

# Read a prior cached value if the cwd matches and the TTL has not expired.
if [ -f "$STATE_FILE" ]; then
	cached_dir=$(sed -n '1p' "$STATE_FILE" 2>/dev/null || true)
	cached_time=$(sed -n '2p' "$STATE_FILE" 2>/dev/null || true)
	cached_value=$(sed -n '3p' "$STATE_FILE" 2>/dev/null || true)

	if [ "$cached_dir" = "$DIR" ] && [ -n "$cached_time" ]; then
		if [ $((now - cached_time)) -lt "${TTL:-60}" ]; then
			[ -n "$cached_value" ] && echo "$cached_value"
			exit 0
		fi
	fi
fi

# Cache miss, cwd changed, or TTL expired: fetch fresh and write state.
value=$(fetch_label) || value=""
{
	echo "$DIR"
	echo "$now"
	echo "$value"
} > "$STATE_FILE"

[ -n "$value" ] && echo "$value"
