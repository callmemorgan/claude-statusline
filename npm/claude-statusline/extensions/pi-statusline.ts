import { spawn } from "node:child_process";
import process from "node:process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

interface Payload {
	cwd: string;
	session_id: string;
	conversation_id: string;
	model?: { id?: string; display_name?: string };
	workspace: { current_dir: string; project_dir: string };
	context_window?: {
		used_percentage?: number | null;
		remaining_percentage?: number | null;
		context_window_size?: number | null;
		current_usage?: Record<string, number> | null;
	};
	cost?: { total_cost_usd?: number | null };
	version?: string | null;
	output_style?: { name?: string };
}

interface ContextUsage {
	percent?: number;
	contextWindow?: number;
}

interface PiModel {
	id?: string;
	displayName?: string;
	name?: string;
	contextWindow?: number;
}

interface PiContext {
	hasUI?: boolean;
	mode?: string;
	cwd?: string;
	getContextUsage?: () => ContextUsage;
	model?: PiModel;
	sessionManager?: { getSessionId?: () => string };
	ui: {
		setFooter: (
			factory?: () => {
				render: (width: number) => string[];
				invalidate: () => void;
			},
		) => void;
	};
}

function resolveCommand(): string {
	if (process.env.CLAUDE_STATUSLINE_BIN) {
		return process.env.CLAUDE_STATUSLINE_BIN;
	}
	try {
		return require.resolve("../bin/claude-statusline.js");
	} catch {
		return "claude-statusline";
	}
}

function buildPayload(ctx: PiContext): Payload {
	const cwd = ctx.cwd ?? process.cwd();
	const contextUsage = ctx.getContextUsage?.() ?? {};
	const model = ctx.model ?? {};
	const sessionId = ctx.sessionManager?.getSessionId?.() ?? cwd;

	return {
		cwd,
		session_id: `pi:${sessionId}`,
		// session_id keys per-session state; conversation_id is the field the
		// session-name segment actually reads, so set both to the same value.
		conversation_id: `pi:${sessionId}`,
		model: {
			id: model.id,
			display_name: model.displayName ?? model.name ?? model.id,
		},
		workspace: {
			current_dir: cwd,
			project_dir: cwd,
		},
		context_window: {
			used_percentage: contextUsage.percent ?? null,
			remaining_percentage:
				typeof contextUsage.percent === "number"
					? Math.max(0, 100 - contextUsage.percent)
					: null,
			context_window_size:
				contextUsage.contextWindow ?? model.contextWindow ?? null,
			current_usage: null,
		},
		cost: { total_cost_usd: null },
		version: null,
		output_style: { name: "default" },
	};
}

async function runCommand(
	command: string,
	payload: Payload,
): Promise<string[]> {
	return new Promise((resolve) => {
		const child = spawn(command, [], {
			stdio: ["pipe", "pipe", "pipe"],
			shell: false,
			env: process.env,
		});

		let stdout = "";
		let settled = false;

		const finish = (lines: string[]) => {
			if (settled) return;
			settled = true;
			resolve(lines);
		};

		child.stdout.on("data", (chunk) => {
			stdout += String(chunk);
		});
		child.stderr.on("data", () => {});
		child.on("error", () => finish([]));
		child.on("close", (code) => {
			if (code !== 0) return finish([]);
			const text = stdout.trim().replace(/\r\n/g, "\n");
			finish(text ? text.split("\n") : []);
		});

		child.stdin.write(JSON.stringify(payload));
		child.stdin.end();
	});
}

function truncateAnsiLine(line: string, maxWidth: number): string {
	if (maxWidth <= 0) return "";
	let visible = 0;
	let output = "";
	let i = 0;
	while (i < line.length && visible < maxWidth) {
		if (line.charCodeAt(i) === 0x1b && line[i + 1] === "[") {
			let j = i + 2;
			while (
				j < line.length &&
				!(line.charCodeAt(j) >= 0x40 && line.charCodeAt(j) <= 0x7e)
			) {
				j++;
			}
			output += line.slice(i, j + 1);
			i = j + 1;
			continue;
		}
		output += line[i];
		visible++;
		i++;
	}
	return output;
}

export default function (pi: ExtensionAPI) {
	const command = resolveCommand();
	let lastLines: string[] = [];
	let lastPayloadJson = "";

	async function refresh(ctx: PiContext): Promise<void> {
		if (!ctx.hasUI || ctx.mode !== "tui") return;

		const payload = buildPayload(ctx);
		const payloadJson = JSON.stringify(payload);
		if (payloadJson === lastPayloadJson && lastLines.length > 0) return;

		const lines = await runCommand(command, payload);
		lastPayloadJson = payloadJson;
		lastLines = lines;

		ctx.ui.setFooter(() => ({
			render(width: number) {
				return lines.map((line) => truncateAnsiLine(line, width));
			},
			invalidate() {},
		}));
	}

	const events: Array<Parameters<ExtensionAPI["on"]>[0]> = [
		"session_start",
		"turn_end",
		"model_select",
		"session_compact",
		"session_tree",
		"agent_start",
		"agent_end",
	];

	for (const event of events) {
		pi.on(event, async (_event, ctx: PiContext) => {
			await refresh(ctx);
		});
	}

	pi.on("session_shutdown", (_event, ctx: PiContext) => {
		ctx.ui.setFooter(undefined);
		lastLines = [];
		lastPayloadJson = "";
	});
}
