// Initial JS budget (design §7.9): 200 KB gzip for the entry chunk plus every
// chunk index.html preloads. Lazy route chunks are not counted. Run after
// `npm run build`.
import { existsSync, readFileSync } from 'node:fs'

const html = existsSync('dist/index.html') ? readFileSync('dist/index.html', 'utf8') : ''
const initial = [...html.matchAll(/(?:src|href)="\/(_ui\/[^"]+\.js)"/g)].map((m) => `dist/${m[1]}`)

export default [
  {
    name: 'initial JS (entry + modulepreload)',
    path: initial.length ? initial : ['dist/_ui/index-*.js'],
    gzip: true,
    limit: '200 kB',
  },
]
