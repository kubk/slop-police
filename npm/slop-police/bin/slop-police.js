#!/usr/bin/env node
import { spawnSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { findBinary } from '../lib/binary.js'

let binary
try {
  binary = findBinary()
} catch (error) {
  console.error(error.message)
  process.exit(2)
}

const root = process.cwd()
const plugins = configuredPlugins(root)
if (plugins) {
  // Plugin rules run here, in Node, with the binary serving ASTs and types.
  const { run } = await import('../lib/runner.js')
  process.exitCode = await run(binary, root, plugins)
} else {
  const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' })
  if (result.error) throw result.error
  process.exitCode = result.status ?? 1
}

// configuredPlugins returns the config's plugins, if it has any. Any problem
// with the config is left to the binary to report.
function configuredPlugins(root) {
  try {
    const { plugins } = JSON.parse(fs.readFileSync(path.join(root, 'slop-police.json'), 'utf8'))
    if (
      Array.isArray(plugins) &&
      plugins.length > 0 &&
      plugins.every((plugin) => typeof plugin === 'string')
    ) {
      return plugins
    }
  } catch {}
  return undefined
}
