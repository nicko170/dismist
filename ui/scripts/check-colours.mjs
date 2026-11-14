// Design §7.2: components use tokens only. Fails when a hex colour appears in
// ui/src outside styles/ (generated files are skipped).
import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '../src')
const skip = new Set([path.join(root, 'styles'), path.join(root, 'gen'), path.join(root, 'routeTree.gen.ts')])
const hex = /(?<![\w&/-])#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})(?![\w-])/g
const failures = []

function walk(dir) {
  for (const name of readdirSync(dir)) {
    const file = path.join(dir, name)
    if (skip.has(file)) continue
    if (statSync(file).isDirectory()) walk(file)
    else if (/\.(tsx?|css)$/.test(name)) {
      readFileSync(file, 'utf8')
        .split('\n')
        .forEach((line, index) => {
          for (const match of line.matchAll(hex)) failures.push(`${path.relative(root, file)}:${index + 1}: ${match[0]}`)
        })
    }
  }
}

walk(root)
if (failures.length) {
  console.error(`Hex colours outside src/styles (use tokens):\n${failures.join('\n')}`)
  process.exit(1)
}
console.log('check-colours: no hex colours outside src/styles')
