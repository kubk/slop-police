#!/usr/bin/env node
const { spawnSync } = require("node:child_process");

const os = process.platform === "win32" ? "windows" : process.platform;
const platformPackage = `slop-police-${os}-${process.arch}`;
const binaryName = process.platform === "win32" ? "slop-police.exe" : "slop-police";

let binary;
try {
  binary = require.resolve(`${platformPackage}/bin/${binaryName}`);
} catch {
  console.error(
    `slop-police: no binary for ${process.platform}-${process.arch}. The optional dependency ${platformPackage} is missing or unsupported on this platform.`,
  );
  process.exit(2);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
