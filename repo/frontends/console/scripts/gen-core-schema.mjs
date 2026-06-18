#!/usr/bin/env node
/**
 * Generate Core API types for Console from api/openapi/v1.yaml.
 *
 * Applies a tiny syntax normalisation for known OpenAPI YAML issues so
 * openapi-typescript can parse the Core contract without modifying v1.yaml.
 */
import { execSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const consoleRoot = path.resolve(here, '..')
const repoRoot = path.resolve(consoleRoot, '../..')
const source = path.join(repoRoot, 'api/openapi/v1.yaml')
const cacheDir = path.join(consoleRoot, '.cache')
const normalized = path.join(cacheDir, 'core-openapi.normalized.yaml')
const output = path.join(consoleRoot, 'src/api/core-schema.d.ts')

let yaml = fs.readFileSync(source, 'utf8')
yaml = yaml.replace('secondary_color:{ type:', 'secondary_color: { type:')

fs.mkdirSync(cacheDir, { recursive: true })
fs.writeFileSync(normalized, yaml)

execSync(`npx openapi-typescript "${normalized}" -o "${output}"`, {
  cwd: consoleRoot,
  stdio: 'inherit',
})

console.log(`✅ Core API types → ${path.relative(consoleRoot, output)}`)
