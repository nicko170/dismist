#!/usr/bin/env node
// Live config flows (design §7.10) against a running dismistd with
// tools/fakeap connected: create a WLAN, edit it with the dry-run preview,
// change the site's 5 GHz width, an AP's LED brightness and the site name,
// then restore an earlier revision. Every API mutation and its status is
// printed; it exits 1 on a failed step.
//
//   BASE_URL=… TOKEN=… SITE=… AP=… [OUT=dir] node scripts/live-flows.mjs
import { mkdirSync } from 'node:fs'
import path from 'node:path'
import { chromium, expect } from '@playwright/test'

const { BASE_URL: base, TOKEN: token, SITE: site, AP: ap, OUT: out } = process.env
if (!base || !token || !site || !ap) {
  console.error('BASE_URL, TOKEN, SITE and AP are required')
  process.exit(2)
}
const browser = await chromium.launch()
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } })
await context.request.post(`${base}/auth/login`, { data: { token }, headers: { Origin: base } })
const page = await context.newPage()
const calls = []
page.on('response', async (response) => {
  const request = response.request()
  const url = new URL(response.url())
  if (!url.pathname.startsWith('/api/') || request.method() === 'GET') return
  let detail = ''
  if (response.status() >= 400) {
    detail = `\n         sent ${(request.postData() ?? '').slice(0, 600)}\n         got  ${(await response.text().catch(() => '')).slice(0, 400)}`
  }
  calls.push(`${request.method()} ${url.pathname}${url.search} -> ${response.status()}${detail}`)
})
const errors = []
page.on('pageerror', (error) => errors.push(error.message))
let failed = 0
const shot = async (name) => {
  if (!out) return
  mkdirSync(out, { recursive: true })
  await page.screenshot({ path: path.join(out, `flow-${name}.png`), fullPage: true })
}
async function step(name, run) {
  calls.length = 0
  try {
    await run()
    console.log(`ok   ${name}\n       ${calls.join('\n       ')}`)
  } catch (error) {
    failed++
    console.log(`FAIL ${name}: ${error.message.split('\n')[0]}\n       ${calls.join('\n       ')}`)
    await shot(`${name}-failed`)
  }
}
const saveBar = () => page.getByRole('region', { name: 'Save changes' })

await step('wlan-create', async () => {
  await page.goto(`${base}/sites/${site}/wlans/new`)
  await page.getByLabel('SSID', { exact: true }).fill('Example-Guest')
  await page.getByLabel(/^Passphrase/).first().fill('guest-passphrase-1')
  await page.getByRole('button', { name: 'Create WLAN' }).click()
  await page.waitForURL(/\/wlans\/[0-9a-f-]{36}/, { timeout: 15000 })
  await shot('wlan-created')
})

await step('wlan-edit-dry-run-and-save', async () => {
  await page.getByRole('switch', { name: /Hidden SSID/ }).click()
  await expect(saveBar()).toBeVisible()
  // The bar shows the dry run's impact line (§4.8).
  await expect(saveBar()).toContainText(/AP|No AP changes/, { timeout: 10000 })
  await shot('wlan-dry-run')
  await saveBar().getByRole('button', { name: 'Save' }).click()
  const confirm = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  if (await confirm.isVisible().catch(() => false)) await confirm.getByRole('button', { name: /Save|Apply|Continue/ }).first().click()
  await expect(saveBar()).toBeHidden({ timeout: 15000 })
})

await step('rf-width', async () => {
  await page.goto(`${base}/sites/${site}/rf`)
  await page.getByLabel('5 GHz width').selectOption('40')
  await expect(saveBar()).toContainText(/AP/, { timeout: 10000 })
  await shot('rf-dry-run')
  await saveBar().getByRole('button', { name: 'Save' }).click()
  const confirm = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  if (await confirm.isVisible().catch(() => false)) await confirm.getByRole('button', { name: /Save|Apply|Continue|Restart/ }).first().click()
  await expect(saveBar()).toBeHidden({ timeout: 15000 })
})

await step('ap-led', async () => {
  await page.goto(`${base}/sites/${site}/aps/${ap}/settings`)
  await page.getByText('LED brightness').first().scrollIntoViewIfNeeded()
  // The first "Override for this AP" switch is the LED brightness row.
  await page.getByRole('switch', { name: 'Override for this AP' }).first().click()
  await expect(saveBar()).toBeVisible({ timeout: 10000 })
  await shot('ap-led-dry-run')
  await saveBar().getByRole('button', { name: 'Save' }).click()
  const confirm = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  if (await confirm.isVisible().catch(() => false)) await confirm.getByRole('button', { name: /Save|Apply|Continue/ }).first().click()
  await expect(saveBar()).toBeHidden({ timeout: 15000 })
})

await step('site-name', async () => {
  await page.goto(`${base}/sites/${site}/settings/general`)
  const name = page.getByLabel(/^Name|Site name/).first()
  await name.fill('Home Base')
  await saveBar().getByRole('button', { name: 'Save' }).click()
  await expect(saveBar()).toBeHidden({ timeout: 15000 })
})

await step('changes-restore', async () => {
  await page.goto(`${base}/sites/${site}/changes`)
  await shot('changes-list')
  // The hidden-SSID edit is the second newest config change; restore the
  // revision before the RF and AP edits.
  await page.getByText(/hidden changed/).first().locator('xpath=ancestor::*[.//a][1]').getByRole('link').first().click()
  await page.getByRole('button', { name: 'Restore this revision' }).click()
  await expect(page.getByText(/Changed back|No AP changes|AP/).first()).toBeVisible({ timeout: 10000 })
  await shot('restore-preview')
  await page.getByRole('button', { name: /^Restore revision/ }).last().click()
  await page.waitForTimeout(2000)
})

await step('wlan-reveal', async () => {
  await page.goto(`${base}/sites/${site}/wlans`)
  await page.getByRole('link', { name: /Example-Guest/ }).first().click()
  await page.getByRole('button', { name: 'Wi-Fi QR' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await shot('wlan-qr')
})

if (errors.length) {
  failed++
  console.log(`page errors:\n  ${errors.join('\n  ')}`)
}
await browser.close()
process.exit(failed ? 1 : 0)
