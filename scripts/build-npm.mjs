// Builds the npm packages into dist/npm: one package per platform with the
// prebuilt binary, plus the main slop-police package that depends on all of
// them optionally, so npm installs only the one matching the user's machine.
//
//   node scripts/build-npm.mjs                  all platforms
//   node scripts/build-npm.mjs darwin-arm64     only the listed platforms
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const dist = path.join(root, "dist", "npm");
const main = JSON.parse(
  fs.readFileSync(path.join(root, "npm/slop-police/package.json"), "utf8"),
);

// Windows packages are named "windows-*" rather than Node's "win32-*": npm's
// spam detection rejected the "win32-arm64" name.
const platforms = [
  { name: "darwin-arm64", os: "darwin", cpu: "arm64", goos: "darwin", goarch: "arm64", label: "macOS ARM64" },
  { name: "darwin-x64", os: "darwin", cpu: "x64", goos: "darwin", goarch: "amd64", label: "macOS x64" },
  { name: "linux-arm64", os: "linux", cpu: "arm64", goos: "linux", goarch: "arm64", label: "Linux ARM64" },
  { name: "linux-x64", os: "linux", cpu: "x64", goos: "linux", goarch: "amd64", label: "Linux x64" },
  { name: "windows-arm64", os: "win32", cpu: "arm64", goos: "windows", goarch: "arm64", label: "Windows ARM64" },
  { name: "windows-x64", os: "win32", cpu: "x64", goos: "windows", goarch: "amd64", label: "Windows x64" },
];

const only = process.argv.slice(2);
for (const name of only) {
  if (!platforms.some((platform) => platform.name === name)) {
    throw new Error(`Unknown platform: ${name}`);
  }
}

fs.rmSync(dist, { recursive: true, force: true });

for (const platform of platforms) {
  if (only.length > 0 && !only.includes(platform.name)) continue;

  const dir = path.join(dist, `slop-police-${platform.name}`);
  const binary = path.join(
    dir,
    "bin",
    platform.os === "win32" ? "slop-police.exe" : "slop-police",
  );
  console.log(`Building ${platform.name}`);
  execFileSync(
    "go",
    ["build", "-trimpath", "-ldflags=-s -w", "-o", binary, "./cmd/slop-police"],
    {
      cwd: root,
      stdio: "inherit",
      env: {
        ...process.env,
        CGO_ENABLED: "0",
        GOOS: platform.goos,
        GOARCH: platform.goarch,
      },
    },
  );

  writeJson(path.join(dir, "package.json"), {
    name: `slop-police-${platform.name}`,
    version: main.version,
    description: `The ${platform.label} binary for slop-police`,
    license: main.license,
    repository: main.repository,
    os: [platform.os],
    cpu: [platform.cpu],
    files: ["bin", "LICENSE", "THIRD_PARTY_NOTICES.txt"],
  });
  fs.copyFileSync(path.join(root, "LICENSE"), path.join(dir, "LICENSE"));
  fs.writeFileSync(
    path.join(dir, "THIRD_PARTY_NOTICES.txt"),
    thirdPartyNotices(binary),
  );
}

const mainDir = path.join(dist, "slop-police");
fs.cpSync(path.join(root, "npm/slop-police"), mainDir, { recursive: true });
writeJson(path.join(mainDir, "package.json"), {
  ...main,
  files: [...main.files, "README.md", "LICENSE"],
  optionalDependencies: Object.fromEntries(
    platforms.map(({ name }) => [`slop-police-${name}`, main.version]),
  ),
});
fs.copyFileSync(path.join(root, "README.md"), path.join(mainDir, "README.md"));
fs.copyFileSync(path.join(root, "LICENSE"), path.join(mainDir, "LICENSE"));

function writeJson(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

// The binary statically links the Go runtime and every module it imports
// (including the TypeScript compiler and its lib.d.ts files), so their
// licenses ship next to it.
function thirdPartyNotices(binary) {
  const goEnv = (name) =>
    execFileSync("go", ["env", name], { encoding: "utf8" }).trim();
  const modCache = goEnv("GOMODCACHE");
  const goRoot = goEnv("GOROOT");
  const sections = [
    // Homebrew's Go keeps LICENSE next to GOROOT instead of inside it.
    {
      name: "Go",
      dir: fs.existsSync(path.join(goRoot, "LICENSE")) ? goRoot : path.dirname(goRoot),
    },
    ...execFileSync("go", ["version", "-m", binary], { encoding: "utf8" })
      .split("\n")
      .map((line) => line.trim().split(/\s+/))
      .filter(([kind]) => kind === "dep")
      .map(([, module, version]) => ({
        name: `${module} ${version}`,
        dir: path.join(
          modCache,
          `${module.replace(/[A-Z]/g, (c) => `!${c.toLowerCase()}`)}@${version}`,
        ),
      })),
  ];

  return sections
    .map(({ name, dir }) => {
      const files = fs
        .readdirSync(dir)
        .filter((file) => /^(licen[cs]e|notice|copying)(\.(txt|md))?$/i.test(file))
        .sort();
      if (files.length === 0) throw new Error(`No license file for ${name}`);
      const texts = files.map((file) =>
        fs.readFileSync(path.join(dir, file), "utf8").trim(),
      );
      return [`${name}\n${"=".repeat(name.length)}`, ...texts].join("\n\n");
    })
    .join("\n\n\n");
}
