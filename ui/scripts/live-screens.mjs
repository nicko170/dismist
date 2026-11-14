#!/usr/bin/env node
// Live screen sweep: signs in to a running dismistd (with tools/fakeap
// connected), opens every screen at 390 and 1440 px wide, records API
// responses of 400 and above, console errors and page errors, and
// optionally saves a screenshot of each screen (evidence/v1-screens). The
// asserted end-to-end flows are e2e/harness-live/*.spec.ts (@live, run in
// CI by the ui-e2e-live job); this script only takes the screenshots.
//
//   BASE_URL=http://127.0.0.1:18488 TOKEN=… SITE=… AP=… CLIENT=… WLAN=… REV=… \
//     [OUT=dir] node scripts/live-screens.mjs
//
// It exits 1 when any screen shows an unexpected failure. Only loopback
// test controllers: it never contacts anything else.
import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { chromium } from '@playwright/test'

const base = process.env.BASE_URL
const token = process.env.TOKEN
const site = process.env.SITE
const ap = process.env.AP
const client = process.env.CLIENT ?? ''
const wlan = process.env.WLAN ?? ''
const rev = process.env.REV ?? ''
const out = process.env.OUT
if (!base || !token || !site || !ap) {
  console.error('BASE_URL, TOKEN, SITE and AP are required')
  process.exit(2)
}

const screens = [
  ['login', '/login', { signedOut: true }],
  ['sites', '/sites'],
  ['site-overview', `/sites/${site}/overview`],
  ['ap-list', `/sites/${site}/aps`],
  ['ap-overview', `/sites/${site}/aps/${ap}`],
  ['ap-radios', `/sites/${site}/aps/${ap}/radios`],
  ['ap-clients', `/sites/${site}/aps/${ap}/clients`],
  ['ap-uplink', `/sites/${site}/aps/${ap}/uplink`],
  ['ap-events', `/sites/${site}/aps/${ap}/events`],
  ['ap-log', `/sites/${site}/aps/${ap}/events?kind=log`],
  ['ap-settings', `/sites/${site}/aps/${ap}/settings`],
  ['ap-config', `/sites/${site}/aps/${ap}/config`],
  ['clients', `/sites/${site}/clients`],
  ...(client ? [['client-detail', `/sites/${site}/clients/${client}`]] : []),
  ['events', `/sites/${site}/events`],
  ['wlans', `/sites/${site}/wlans`],
  ['wlan-new', `/sites/${site}/wlans/new`],
  ...(wlan ? [['wlan-edit', `/sites/${site}/wlans/${wlan}`], ['wlan-expert', `/sites/${site}/wlans/${wlan}/expert`]] : []),
  ['rf', `/sites/${site}/rf`],
  ['rf-neighbours', `/sites/${site}/rf/neighbours`],
  ['site-settings-general', `/sites/${site}/settings/general`],
  ['site-settings-access', `/sites/${site}/settings/access`],
  ['site-settings-network', `/sites/${site}/settings/network`],
  ['site-settings-ap-defaults', `/sites/${site}/settings/ap-defaults`],
  ['site-settings-protection', `/sites/${site}/settings/protection`],
  ['site-settings-advanced', `/sites/${site}/settings/advanced`],
  ['site-settings-danger', `/sites/${site}/settings/danger`],
  ['changes', `/sites/${site}/changes`],
  ...(rev ? [['change-detail', `/sites/${site}/changes/${rev}`]] : []),
  ['system', '/system'],
  ['system-backups', '/system/backups'],
  ['system-tokens', '/system/tokens'],
  ['system-sessions', '/system/sessions'],
  ['system-migration', '/system/migration'],
  ['onboard', '/onboard'],
  ['onboard-register', `/onboard/register?site=${site}`],
]

// Answers that are the contract, not failures: unverified ap-cmd-rx actions.
// Signed out, the session probe answers 401 by design.
const expected = [/\/actions\/(reboot|restart-radios)$/, /\/disconnect$/, /^\/auth\/session$/]

const widths = [
  ['390', { width: 390, height: 844 }, true],
  ['1440', { width: 1440, height: 900 }, false],
]

const browser = await chromium.launch()
const report = []
let failures = 0
for (const [label, viewport, mobile] of widths) {
  const context = await browser.newContext({ viewport, isMobile: mobile, hasTouch: mobile, deviceScaleFactor: mobile ? 2 : 1 })
  const signIn = await context.request.post(`${base}/auth/login`, { data: { token }, headers: { Origin: base } })
  if (!signIn.ok()) throw new Error(`login ${signIn.status()}`)
  for (const [name, route, options] of screens) {
    const page = options?.signedOut ? await (await browser.newContext({ viewport, isMobile: mobile, hasTouch: mobile })).newPage() : await context.newPage()
    const problems = []
    page.on('pageerror', (error) => problems.push(`pageerror: ${error.message}`))
    page.on('console', (message) => {
      if (message.type() === 'error' && !/Failed to load resource/.test(message.text())) problems.push(`console: ${message.text().slice(0, 300)}`)
    })
    page.on('response', (response) => {
      const url = new URL(response.url())
      if (!url.pathname.startsWith('/api/') && !url.pathname.startsWith('/auth/')) return
      if (response.status() >= 400 && !expected.some((pattern) => pattern.test(url.pathname))) {
        problems.push(`${response.request().method()} ${url.pathname}${url.search} -> ${response.status()}`)
      }
    })
    try {
      await page.goto(`${base}${route}`, { waitUntil: 'domcontentloaded' })
      await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {})
      await page.waitForTimeout(1200)
      const crashed = await page.getByText(/Something went wrong|Unexpected Application Error|Cannot read properties/).count()
      if (crashed) problems.push('error boundary shown')
      if (out) {
        mkdirSync(out, { recursive: true })
        await page.screenshot({ path: path.join(out, `${name}-${label}.png`), fullPage: true })
      }
    } catch (error) {
      problems.push(`navigation: ${error.message}`)
    }
    failures += problems.length ? 1 : 0
    report.push({ screen: name, width: label, url: page.url().replace(base, ''), problems })
    console.log(`${problems.length ? 'FAIL' : 'ok  '} ${label.padStart(4)} ${name}${problems.length ? '\n       ' + problems.join('\n       ') : ''}`)
    await page.close()
  }
  await context.close()
}
await browser.close()
if (out) writeFileSync(path.join(out, 'report.json'), JSON.stringify(report, null, 2))
process.exit(failures ? 1 : 0)
