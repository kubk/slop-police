// Compares the speed of the working tree's binary with a git ref's (HEAD by
// default) on projects with a slop-police.json. The binaries run in turns, so
// that machine load affects both alike, and their outputs must match.
//
//   node scripts/bench.mjs [--base <ref>] [--runs <n>] <dir>...
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";

const { values, positionals: dirs } = parseArgs({
  allowPositionals: true,
  options: { base: { type: "string", default: "HEAD" }, runs: { type: "string", default: "15" } },
});
const runs = Number(values.runs);
if (dirs.length === 0 || !(runs > 0)) {
  console.error("usage: node scripts/bench.mjs [--base <ref>] [--runs <n>] <dir>...");
  process.exit(2);
}

const root = path.resolve(import.meta.dirname, "..");
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "slop-police-bench-"));
try {
  const build = (dir, out) => execFileSync("go", ["build", "-o", out, "./cmd/slop-police"], { cwd: dir, stdio: "inherit" });
  const base = path.join(tmp, "base");
  const worktree = path.join(tmp, "worktree");
  execFileSync("git", ["worktree", "add", "--detach", "--quiet", worktree, values.base], { cwd: root });
  try {
    build(worktree, base);
  } finally {
    execFileSync("git", ["worktree", "remove", "--force", worktree], { cwd: root });
  }
  const current = path.join(tmp, "current");
  build(root, current);

  console.log(`${runs} runs each, ${values.base} vs working tree (median, middle half)\n`);
  for (const dir of dirs) {
    const run = (binary) => {
      const start = process.hrtime.bigint();
      const result = spawnSync(binary, { cwd: dir, encoding: "utf8" });
      const ms = Number(process.hrtime.bigint() - start) / 1e6;
      return { ms, output: `exit ${result.status}\n${result.stdout}${result.stderr}` };
    };
    // Warms the file cache.
    const expected = run(base).output;
    if (run(current).output !== expected) {
      console.log(`${dir}: the outputs differ, run both binaries there to compare`);
      continue;
    }
    const times = { base: [], current: [] };
    for (let i = 0; i < runs; i++) {
      for (const [name, binary] of i % 2 ? [["current", current], ["base", base]] : [["base", base], ["current", current]]) {
        times[name].push(run(binary).ms);
      }
    }
    const [b, c] = [stats(times.base), stats(times.current)];
    const change = ((c.median - b.median) / b.median) * 100;
    console.log(path.basename(path.resolve(dir)));
    console.log(`  ${values.base.padEnd(12)} ${format(b)}`);
    console.log(`  ${"working tree".padEnd(12)} ${format(c)}  ${change >= 0 ? "+" : ""}${change.toFixed(1)}%`);
  }
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}

function stats(ms) {
  const sorted = [...ms].sort((a, b) => a - b);
  const at = (q) => sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))];
  return { median: at(0.5), low: at(0.25), high: at(0.75) };
}

function format({ median, low, high }) {
  return `${median.toFixed(0).padStart(6)}ms  (${low.toFixed(0)}–${high.toFixed(0)})`;
}
