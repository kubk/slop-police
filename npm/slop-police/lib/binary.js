import { createRequire } from "node:module";

// findBinary returns the path of the prebuilt binary from the platform package
// npm installed for this machine. SLOP_POLICE_BINARY overrides it, for
// developing slop-police itself.
export function findBinary() {
  if (process.env.SLOP_POLICE_BINARY) {
    return process.env.SLOP_POLICE_BINARY;
  }
  const platformPackage = `slop-police-${process.platform}-${process.arch}`;
  try {
    return createRequire(import.meta.url).resolve(`${platformPackage}/bin/slop-police`);
  } catch {
    throw new Error(
      `slop-police: no binary for ${process.platform}-${process.arch}. Supported: macOS and Linux on arm64 and x64 (use WSL on Windows).`,
    );
  }
}
