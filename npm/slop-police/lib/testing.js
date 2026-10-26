import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { findBinary } from './binary.js'
import { lint as lintWithRules } from './runner.js'

const tsconfig = {
  compilerOptions: {
    strict: true,
    target: 'ES2022',
    module: 'ESNext',
    moduleResolution: 'Bundler',
    jsx: 'preserve',
    allowImportingTsExtensions: true,
    noEmit: true,
  },
  include: ['**/*.ts', '**/*.tsx'],
}

// lint runs rule on source (as index.ts) or on files (paths to contents, with
// a default strict tsconfig.json unless one is given) and returns its
// findings, leaving out those of the built-in rules.
export function lint(rule, source, options) {
  const files = typeof source === 'string' ? { 'index.ts': source } : source
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'slop-police-test-'))
  try {
    const config = {
      projects: ['tsconfig.json'],
      rules: { [rule.name]: options ? ['error', options] : 'error' },
    }
    const all = {
      'tsconfig.json': JSON.stringify(tsconfig),
      ...files,
      'slop-police.json': JSON.stringify(config),
    }
    for (const [name, content] of Object.entries(all)) {
      const file = path.join(root, name)
      fs.mkdirSync(path.dirname(file), { recursive: true })
      fs.writeFileSync(file, content)
    }
    return lintWithRules(findBinary(), root, [rule])
      .results.filter((result) => result.rule === rule.name)
      .map(({ file, line, column, message }) => ({ file, line, column, message }))
  } finally {
    fs.rmSync(root, { recursive: true, force: true })
  }
}
