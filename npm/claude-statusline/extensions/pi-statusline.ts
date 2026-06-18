/**
 * claude-statusline — pi extension shim.
 *
 * Bridges pi's footer (ctx.ui.setFooter) to the existing Go renderer binary.
 * Each render tick synthesizes a Claude-Code-shaped JSON payload from pi's
 * data model (ctx.model, ctx.sessionManager.getBranch(), ctx.getContextUsage(),
 * footerData.getGitBranch()), feeds it to the bundled Go binary on stdin, and
 * returns the binary's stdout split into lines.
 *
 * v1 tradeoff (agreed): spawn-per-render. The Go binary reads exactly one JSON
 * object from stdin (see payload.go:readInput), so this is a clean stdin→stdout
 * round-trip. Caching by input signature avoids respawning when nothing
 * changed. Render triggers are debounced (200ms trailing) so bursts — model
 * cycling via Ctrl+P, rapid message_end during agentic tool loops — coalesce
 * into a single spawn instead of one spawn per event. The follow-up `pi-serve`
 * long-lived mode will eliminate the per-render spawn entirely.
 *
 * Segments with no pi data source are left empty so the binary's auto-hide rule
 * suppresses them: rate_limits, vim, effort, output_style, agent, artifact_count,
 * plan_tier, sandbox, worktree.name, cost line/duration counters.
 *
 * Installed as a pi package: `pi install npm:@morgan.rebrand/claude-statusline`.
 * The Go binary is pulled in via the main package's platform optionalDependencies
 * (same resolution the CLI shim uses), so this one install provides both.
 *
 * Theming note: the Go binary owns its own colors via ~/.config/claude-statusline/config.toml
 * and does NOT follow pi's active theme. Only the error-fallback line uses pi's
 * theme. If the statusline clips on narrow terminals, set a wrap mode
 * (e.g. `wrap = 'cascade'`) in config.toml so the binary respects terminal_width;
 * otherwise the default `off` emits unwrapped logical lines and the safety
 * truncate below will clip the right edge.
 *
 * Listener lifecycle: pi.on(event, handler) returns void and offers no
 * unsubscribe — handlers live for the extension's lifetime. We register them
 * once (lazily) and guard them with `activeTui` so they no-op whenever the
 * footer is disabled. The git-branch subscription from footerData DOES return
 * an unsubscribe, so it's cleaned up in dispose(). ctx.sessionManager/ctx.model/
 * ctx.cwd are live getters over runner.*, so the footer follows session/tree/
 * fork switches without re-registration.
 */

import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { truncateToWidth } from "@earendil-works/pi-tui";

const PKG_FAMILY = "@morgan.rebrand/claude-statusline-";

// Mirrors npm/claude-statusline/bin/claude-statusline.js — keep in sync.
const PLATFORM_PACKAGES: Record<string, string> = {
	"darwin/arm64": "darwin-arm64",
	"darwin/x64": "darwin-x64",
	"linux/arm64": "linux-arm64",
	"linux/x64": "linux-x64",
	"win32/x64": "win32-x64",
};

// Resolve a module id the ESM-safe way. createRequire(import.meta.url) works in
// pi's tsx ESM loader; the global `require` fallback covers any CJS loader.
// Memoized so the require-creation cost is paid once.
const moduleRequire = (() => {
	try {
		return createRequire(import.meta.url);
	} catch {
		// tsx injects a CJS `require` in this case.
		return require;
	}
})();

function resolveBinary(): string | null {
	const envBin = process.env.CLAUDE_STATUSLINE_BIN;
	if (envBin) return envBin;

	const key = `${process.platform}/${process.arch}`;
	const suffix = PLATFORM_PACKAGES[key];
	if (!suffix) return null;

	const binName = process.platform === "win32" ? "claude-statusline.exe" : "claude-statusline";
	try {
		return moduleRequire.resolve(`${PKG_FAMILY}${suffix}/bin/${binName}`);
	} catch {
		return null;
	}
}

// Build the Claude-Code-shaped payload the Go binary expects (payload.go).
// Only fields with a pi data source are populated; the rest stay empty/zero
// and the corresponding segments auto-hide.
// biome-ignore lint/suspicious/noExplicitAny: ctx/footerData are pi-internal types; `any` avoids the AgentMessage union-narrowing dance around session entries.
function buildPayload(ctx: any, footerData: any, width: number): string {
	let inputTokens = 0;
	let outputTokens = 0;
	let totalCost = 0;
	// biome-ignore lint/suspicious/noExplicitAny: usage shape varies across providers; we only read a few numeric fields.
	let lastUsage: any = null;

	for (const e of ctx.sessionManager.getBranch()) {
		if (e?.type === "message" && e.message?.role === "assistant") {
			const u = e.message.usage;
			if (u) {
				inputTokens += u.input ?? 0;
				outputTokens += u.output ?? 0;
				totalCost += u.cost?.total ?? 0;
				lastUsage = u;
			}
		}
	}

	const cu = ctx.getContextUsage();
	const model = ctx.model;
	const branch = footerData.getGitBranch();
	const sessionId = ctx.sessionManager.getSessionId?.() ?? "";
	const sessionName = ctx.sessionManager.getSessionName?.() ?? "";

	const payload: Record<string, unknown> = {
		session_id: sessionId,
		session_name: sessionName,
		cwd: ctx.cwd,
		product: "pi",
		terminal_width: width,
		model: {
			display_name: model?.name ?? "",
			id: model?.id ?? "",
		},
		workspace: {
			current_dir: ctx.cwd,
		},
		// worktree.branch is the only field renderGitBranch reads (segments.go:48).
		// footerData.getGitBranch() returns "detached" for detached HEAD, which
		// the binary will display as-is.
		worktree: { name: "", branch: branch ?? "" },
		cost: {
			total_cost_usd: totalCost,
			total_lines_added: 0,
			total_lines_removed: 0,
			total_duration_ms: 0,
			total_api_duration_ms: 0,
		},
		context_window: {
			total_input_tokens: inputTokens,
			total_output_tokens: outputTokens,
			context_window_size: cu?.contextWindow ?? model?.contextWindow ?? 0,
			used_percentage: cu?.percent ?? null,
			current_usage: lastUsage
				? {
						input_tokens: lastUsage.input ?? 0,
						output_tokens: lastUsage.output ?? 0,
						cache_creation_input_tokens: lastUsage.cacheWrite ?? 0,
						cache_read_input_tokens: lastUsage.cacheRead ?? 0,
					}
				: { input_tokens: 0, output_tokens: 0, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 },
		},
		// rate_limits, vim, effort, output_style, agent: intentionally empty → auto-hide.
		rate_limits: {},
	};

	// exceeds_200k_tokens is a *bool (pointer) in the Go struct; emit only when
	// we have a real token estimate so the segment doesn't render on unknown.
	if (cu?.tokens != null) {
		payload.exceeds_200k_tokens = cu.tokens > 200_000;
	}

	return JSON.stringify(payload);
}

export default function (pi: ExtensionAPI) {
	let enabled = false;

	// Current TUI instance when the footer is active; undefined when disabled.
	// pi.on handlers (registered once, never unregistered) guard against this.
	let activeTui: { requestRender(): void } | undefined;
	let listenersRegistered = false;

	// Lazily register the render-trigger listeners once, on first enable.
	// pi.on returns void and cannot be undone; the activeTui guard makes them
	// no-ops whenever the footer is off. All triggers are debounced so bursts
	// (model cycling, rapid message_end in tool loops) coalesce into one spawn.
	const registerListeners = () => {
		if (listenersRegistered) return;
		listenersRegistered = true;
		pi.on("model_select", () => scheduleRender());
		pi.on("message_end", () => scheduleRender());
		pi.on("thinking_level_select", () => scheduleRender());
		pi.on("turn_end", () => scheduleRender());
	};

	// Trailing-edge debounce. Held in the outer scope so dispose can cancel a
	// pending render; recreated each time the footer is enabled.
	let renderTimer: ReturnType<typeof setTimeout> | undefined;
	const DEBOUNCE_MS = 200;
	const scheduleRender = () => {
		if (!activeTui) return;
		if (renderTimer) clearTimeout(renderTimer);
		renderTimer = setTimeout(() => {
			renderTimer = undefined;
			activeTui?.requestRender();
		}, DEBOUNCE_MS);
	};

	pi.registerCommand("statusline", {
		description: "Toggle the claude-statusline footer in pi",
		handler: async (_args, ctx) => {
			enabled = !enabled;
			if (enabled) {
				ctx.ui.setFooter((tui, theme, footerData) => {
					registerListeners();
					activeTui = tui;

					// git branch is the one render trigger sourced from footerData;
					// its subscription returns an unsubscribe, so clean it up on dispose.
					const unsubBranch = footerData.onBranchChange(() => scheduleRender());

					let cachedSignature = "";
					let cachedLines: string[] | null = null;

					return {
						dispose: () => {
							activeTui = undefined;
							if (renderTimer) {
								clearTimeout(renderTimer);
								renderTimer = undefined;
							}
							unsubBranch();
						},
						invalidate() {
							// Drop the cache so the next render re-spawns. The Go binary
							// owns its own colors via config.toml, so theme changes don't
							// strictly require a respawn — but invalidate is cheap.
							cachedLines = null;
						},
						render(width: number): string[] {
							if (!activeTui) return [];

							const json = buildPayload(ctx, footerData, width);

							// Signature guards the spawn: skip if inputs (incl. width) are
							// unchanged. Avoids respawning on resize/theme/irrelevant
							// requestRender (the event handlers above can fire in bursts,
							// further coalesced by scheduleRender's debounce).
							const sig = `${width}\u0001${json}`;
							if (cachedLines && sig === cachedSignature) {
								return cachedLines;
							}

							const bin = resolveBinary();
							if (!bin) {
								return [
									truncateToWidth(
										theme.fg(
											"error",
											"claude-statusline: binary not found — set CLAUDE_STATUSLINE_BIN or reinstall @morgan.rebrand/claude-statusline",
										),
										width,
									),
								];
							}

							const r = spawnSync(bin, [], {
								input: json,
								encoding: "utf8",
								shell: false,
								maxBuffer: 4 * 1024 * 1024,
							});
							if (r.error || (r.status !== 0 && r.status !== null)) {
								const msg = r.error?.message || r.stderr?.trim() || `exit ${r.status}`;
								return [truncateToWidth(theme.fg("error", `claude-statusline: ${msg}`), width)];
							}

							const out = (r.stdout ?? "").replace(/\r?\n$/, "");
							const lines = out === "" ? [] : out.split(/\r?\n/);

							// Safety net: if the binary emits lines wider than the budget
							// (misconfigured wrap mode, narrow terminal, etc.), truncate
							// rather than break the footer layout. truncateToWidth is
							// ANSI-aware. Note this can clip the rightmost segment when the
							// binary's wrap mode is `off` — set a wrap mode in config.toml
							// to avoid clipping on narrow terminals.
							cachedLines = lines.map((l) => truncateToWidth(l, width));
							cachedSignature = sig;
							return cachedLines;
						},
					};
				});
				ctx.ui.notify("claude-statusline footer enabled", "info");
			} else {
				ctx.ui.setFooter(undefined);
				ctx.ui.notify("claude-statusline footer restored to pi default", "info");
			}
		},
	});
}
