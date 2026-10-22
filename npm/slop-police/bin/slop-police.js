#!/usr/bin/env node
const { spawnSync } = require("node:child_process");

const platformPackage = `slop-police-${process.platform}-${process.arch}`;

let binary;
try {
  binary = require.resolve(`${platformPackage}/bin/slop-police`);
} catch {
  console.error(
    `slop-police: no binary for ${process.platform}-${process.arch}. Supported: macOS and Linux on arm64 and x64 (use WSL on Windows).`,
  );
  process.exit(2);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
