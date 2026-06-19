#!/usr/bin/env node
// Smoke test for the pi extension: loads the TypeScript module with a mock
// ExtensionAPI and confirms it registers a footer that renders at least one line.

import path from "node:path";
import { fileURLToPath } from "node:url";

const extPath = path.resolve(
	path.dirname(fileURLToPath(import.meta.url)),
	"../npm/claude-statusline/extensions/pi-statusline.ts",
);

const handlers = new Map();
const api = {
	on(event, handler) {
		handlers.set(event, handler);
	},
	registerCommand() {},
};

const mod = await import(extPath);
mod.default(api);

const ctx = {
	hasUI: true,
	mode: "tui",
	cwd: process.cwd(),
	model: { id: "claude-sonnet-4", displayName: "Claude Sonnet" },
	getContextUsage: () => ({
		tokens: 50000,
		contextWindow: 200000,
		percent: 25,
	}),
	sessionManager: { getSessionId: () => "test-session" },
	ui: {
		setFooter(factory) {
			const component = factory({}, {}, { getGitBranch: () => "main" });
			const lines = component.render(120);
			if (!Array.isArray(lines) || lines.length === 0) {
				throw new Error("footer factory did not return renderable lines");
			}
			console.log(`ok: ${lines.length} line(s)`);
		},
	},
};

await handlers.get("session_start")?.({}, ctx);
