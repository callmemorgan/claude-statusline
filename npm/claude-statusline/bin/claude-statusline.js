#!/usr/bin/env node
const { spawnSync } = require("child_process");

const pkgFamily = "@morgan.rebrand/claude-statusline-";
const platformPackages = {
	"darwin/arm64": "darwin-arm64",
	"darwin/x64": "darwin-x64",
	"linux/x64": "linux-x64",
	"linux/arm64": "linux-arm64",
	"win32/x64": "win32-x64",
};

function platform() {
	const key = `${process.platform}/${process.arch}`;
	if (platformPackages[key]) return platformPackages[key];
	console.error(
		`claude-statusline: unsupported platform ${key}. Supported: ${Object.keys(platformPackages).join(", ")}.`,
	);
	process.exit(1);
}

function runBinary(bin) {
	const r = spawnSync(bin, process.argv.slice(2), {
		stdio: "inherit",
		shell: false,
	});
	if (r.error) {
		console.error(
			`claude-statusline: could not run ${bin}: ${r.error.message}`,
		);
		process.exit(1);
	}
	process.exit(r.status ?? 1);
}

function main() {
	const bin = process.env.CLAUDE_STATUSLINE_BIN;
	if (bin) {
		runBinary(bin);
	}

	const pkg = pkgFamily + platform();
	const binName =
		process.platform === "win32"
			? "claude-statusline.exe"
			: "claude-statusline";
	let resolved;
	try {
		resolved = require.resolve(`${pkg}/bin/${binName}`);
	} catch (e) {
		if (e && e.code === "MODULE_NOT_FOUND") {
			console.error(
				`claude-statusline: optional dependency ${pkg} is missing. ` +
					`Re-run "npm install -g @morgan.rebrand/claude-statusline", ` +
					`or set CLAUDE_STATUSLINE_BIN to the absolute path of a claude-statusline binary.`,
			);
			process.exit(1);
		}
		throw e;
	}

	runBinary(resolved);
}

main();
