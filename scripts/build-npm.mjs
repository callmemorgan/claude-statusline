#!/usr/bin/env node
import {
  chmodSync,
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from "fs";
import { basename, join } from "path";
import { spawnSync } from "child_process";

const pkgFamily = "@morgan.rebrand/claude-statusline";

const targets = [
  { goos: "darwin", goarch: "arm64", os: "darwin", cpu: "arm64" },
  { goos: "darwin", goarch: "amd64", os: "darwin", cpu: "x64" },
  { goos: "linux", goarch: "amd64", os: "linux", cpu: "x64" },
  { goos: "linux", goarch: "arm64", os: "linux", cpu: "arm64" },
  { goos: "linux", goarch: "arm", os: "linux", cpu: "arm" },
  { goos: "windows", goarch: "amd64", os: "win32", cpu: "x64" },
  { goos: "windows", goarch: "arm64", os: "win32", cpu: "arm64" },
];

function usage() {
  console.error("Usage: scripts/build-npm.mjs <version> <dist-dir>");
  console.error("  version:  git tag or bare version (e.g. v1.2.3 or 1.2.3)");
  console.error("  dist-dir: path to GoReleaser dist/ containing release archives");
  process.exit(1);
}

// Mirrors update.go's assetName and .goreleaser.yaml's name_template exactly.
// A rename in either of those must be reflected here.
function assetName(goos, goarch) {
  const osTitle = goos[0].toUpperCase() + goos.slice(1).toLowerCase();
  let arch = goarch;
  if (arch === "amd64") arch = "x86_64";
  else if (arch === "386") arch = "i386";
  else if (arch === "arm") arch = "armv7";
  const ext = goos === "windows" ? "zip" : "tar.gz";
  return `claude-statusline_${osTitle}_${arch}.${ext}`;
}

function findBinary(dir) {
  const names = ["claude-statusline", "claude-statusline.exe"];
  // GoReleaser archives are flat; look only at the extraction root so an
  // auxiliary file (completion script, etc.) with the same basename can never
  // be mistaken for the binary.
  for (const entry of readdirSync(dir)) {
    if (names.includes(entry)) {
      return join(dir, entry);
    }
  }
  return null;
}

function extractArchive(archivePath, outDir) {
  mkdirSync(outDir, { recursive: true });
  if (archivePath.endsWith(".zip")) {
    const r = spawnSync("unzip", ["-o", archivePath, "-d", outDir], { stdio: "inherit" });
    if (r.status !== 0) throw new Error(`unzip failed for ${archivePath}`);
  } else {
    const r = spawnSync("tar", ["-xzf", archivePath, "-C", outDir], { stdio: "inherit" });
    if (r.status !== 0) throw new Error(`tar failed for ${archivePath}`);
  }
  const bin = findBinary(outDir);
  if (!bin) throw new Error(`binary not found in extracted ${archivePath}`);
  return bin;
}

function platformPackageName(os, cpu) {
  return `${pkgFamily}-${os}-${cpu}`;
}

function smokeTest(binPath, version) {
  const r = spawnSync(binPath, ["version"], { encoding: "utf8", shell: false });
  if (r.error) throw new Error(`smoke test failed for ${binPath}: ${r.error.message}`);
  if (r.status !== 0) throw new Error(`smoke test exited ${r.status} for ${binPath}`);
  if (!r.stdout.includes(version)) {
    throw new Error(`smoke test version mismatch for ${binPath}: expected ${version}, got ${r.stdout}`);
  }
}

function main() {
  const version = (process.argv[2] || "").replace(/^v/, "");
  const distDir = process.argv[3];
  if (!version || !distDir) usage();
  if (!existsSync(distDir)) {
    console.error(`dist directory not found: ${distDir}`);
    process.exit(1);
  }

  const outRoot = "npm";
  mkdirSync(outRoot, { recursive: true });

  // Build per-platform packages.
  const optionalDependencies = {};
  for (const t of targets) {
    const archive = assetName(t.goos, t.goarch);
    const archivePath = join(distDir, archive);
    if (!existsSync(archivePath)) {
      throw new Error(`archive not found: ${archivePath}`);
    }

    const name = platformPackageName(t.os, t.cpu);
    const pkgDir = join(outRoot, name);
    mkdirSync(join(pkgDir, "bin"), { recursive: true });

    const extractDir = join(pkgDir, "_extract");
    const extracted = extractArchive(archivePath, extractDir);
    const binName = t.goos === "windows" ? "claude-statusline.exe" : "claude-statusline";
    const binPath = join(pkgDir, "bin", binName);
    cpSync(extracted, binPath);
    chmodSync(binPath, 0o755);
    rmSync(extractDir, { recursive: true, force: true });

    // Verify the repacked binary runs and reports the expected version — but
    // only for the host's own platform/arch. The publish job runs on a single
    // runner (linux/x64 in CI), where a Darwin/Windows/linux-arm64 binary can't
    // exec; spawning it would fail the build. The other targets are validated
    // upstream by GoReleaser's build and the cosign signature on checksums.txt.
    if (t.os === process.platform && t.cpu === process.arch) {
      smokeTest(binPath, version);
    } else {
      console.log(`  skipping smoke test for ${name} (not runnable on ${process.platform}/${process.arch})`);
    }

    const pkgJson = {
      name,
      version,
      description: "Prebuilt claude-statusline binary for " + `${t.os}/${t.cpu}`,
      license: "MIT",
      // `npm publish --provenance` cross-checks repository.url against the repo
      // the workflow runs in; every package (main + platform) must name the same
      // source repo or the publish 422s. Mirrors the main package's repository.
      repository: {
        type: "git",
        url: "git+https://github.com/callmemorgan/claude-statusline.git",
      },
      os: [t.os],
      cpu: [t.cpu],
      files: ["bin/"],
    };
    writeFileSync(join(pkgDir, "package.json"), JSON.stringify(pkgJson, null, 2) + "\n");

    optionalDependencies[name] = version;
  }

  // Build main package from template.
  const mainTemplate = JSON.parse(readFileSync("npm/claude-statusline/package.json", "utf8"));

  // The committed optionalDependencies list is hand-maintained; fail the build
  // if it has drifted from the actual target set so a reader never sees a stale
  // platform matrix.
  const templateKeys = Object.keys(mainTemplate.optionalDependencies || {}).sort();
  const targetKeys = Object.keys(optionalDependencies).sort();
  if (JSON.stringify(templateKeys) !== JSON.stringify(targetKeys)) {
    throw new Error(
      `template optionalDependencies mismatch: template has [${templateKeys.join(", ")}], ` +
        `targets need [${targetKeys.join(", ")}]`
    );
  }

  const mainPkg = {
    ...mainTemplate,
    version,
    optionalDependencies,
  };
  const mainDir = join(outRoot, pkgFamily);
  mkdirSync(join(mainDir, "bin"), { recursive: true });
  const shimSrc = "npm/claude-statusline/bin/claude-statusline.js";
  const shimDst = join(mainDir, "bin", "claude-statusline.js");
  cpSync(shimSrc, shimDst);
  chmodSync(shimDst, 0o755);
  writeFileSync(join(mainDir, "package.json"), JSON.stringify(mainPkg, null, 2) + "\n");

  console.log(`Built npm packages for v${version} in ${outRoot}/`);
  console.log("Publish order (platform packages first, main package last):");
  for (const name of Object.keys(optionalDependencies)) {
    console.log(`  ${name}`);
  }
  console.log(`  ${pkgFamily}`);
}

main();
