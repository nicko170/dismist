// Renders the PWA and Apple touch icons from the brand mark with Playwright's
// Chromium: `npm run icons` (needs `npx playwright install chromium`).
import { chromium } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import path from 'node:path'

const out = path.resolve(import.meta.dirname, '../public/_ui')
mkdirSync(path.join(out, 'icons'), { recursive: true })

const mark = (size, inset) => `
<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 32 32">
  <rect width="32" height="32" fill="#29323d"/>
  <g transform="translate(${inset} ${inset}) scale(${(32 - 2 * inset) / 32})">
    <g fill="none" stroke="#d65fb0" stroke-linecap="round" stroke-width="2.4">
      <path d="M10 16.5a5.5 5.5 0 0 1 5.5 5.5"/>
      <path d="M10 11.5a10.5 10.5 0 0 1 10.5 10.5" opacity=".8"/>
      <path d="M10 6.5A15.5 15.5 0 0 1 25.5 22" opacity=".6"/>
    </g>
    <circle cx="10" cy="22" r="2.4" fill="#d65fb0"/>
  </g>
</svg>`

const targets = [
  ['icons/icon-192.png', 192, 1],
  ['icons/icon-512.png', 512, 1],
  ['icons/maskable-512.png', 512, 5],
  ['apple-touch-icon.png', 180, 2],
]

const browser = await chromium.launch()
const page = await browser.newPage()
for (const [file, size, inset] of targets) {
  await page.setViewportSize({ width: size, height: size })
  await page.setContent(`<style>html,body{margin:0}</style>${mark(size, inset)}`)
  await page.locator('svg').screenshot({ path: path.join(out, file), omitBackground: false })
  console.log('wrote', file)
}
await browser.close()
