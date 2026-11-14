import type { Page, Response } from '@playwright/test'
import { expect, expectNoHorizontalScroll, test } from '../harness'

// End-to-end flows against a real dismistd with tools/fakeap (design §7.10),
// run by the CI job ui-e2e-live through .github/workflows/scripts/live-harness.sh:
// two registered fake APs on site "Example Home" (synthetic telemetry,
// clients, a kernel log line, a neighbour scan, PACE events every 5 s from
// the first) and a third fake AP waiting in the pending list. Every value
// is synthetic and on loopback; nothing here reaches a real AP.
//
//   .github/workflows/scripts/live-harness.sh -- bash -c 'cd ui && npx playwright test --grep @live --workers=1'
//
// The specs run at every Playwright project width, one after another
// (--workers=1), against the same controller: objects they create carry the
// project name, and assertions do not depend on another project's edits.

const env = {
  base: process.env.E2E_BASE_URL ?? '',
  token: process.env.E2E_ADMIN_TOKEN ?? '',
  site: process.env.E2E_SITE_ID ?? '',
  aps: (process.env.E2E_AP_IDS ?? '').split(' ').filter(Boolean),
  pending: process.env.E2E_PENDING_AP ?? '',
  pendingSecret: process.env.E2E_PENDING_SECRET ?? '',
}

test.skip(!env.token || !env.site || !env.aps.length, 'needs the live harness (E2E_ADMIN_TOKEN, E2E_SITE_ID, E2E_AP_IDS)')

/** Answers that are the contract, not failures (unverified ap-cmd-rx actions; the signed-out session probe). */
const expected = [/\/actions\/(reboot|restart-radios)$/, /\/disconnect$/, /^\/auth\/session$/]

// Sign-ins are rate limited (10 a minute per client, design §4.9), so the
// session cookie is reused while the controller still accepts it; the
// session-expiry flow revokes it and the next sign-in replaces it.
let session: { name: string; value: string } | null = null

async function signIn(page: Page) {
  const context = page.context()
  if (session) {
    await context.addCookies([{ ...session, url: env.base }])
    if ((await page.request.get('/auth/session')).ok()) return
    await context.clearCookies()
  }
  const response = await page.request.post('/auth/login', { data: { token: env.token }, headers: { Origin: env.base } })
  expect(response.ok(), `sign in: ${response.status()}`).toBe(true)
  const cookie = (await context.storageState()).cookies.find((c) => c.httpOnly)
  session = cookie ? { name: cookie.name, value: cookie.value } : null
}

/** Records API failures and page errors while a test runs. */
function watch(page: Page) {
  const problems: string[] = []
  page.on('pageerror', (error) => problems.push(`pageerror: ${error.message}`))
  page.on('response', (response: Response) => {
    const url = new URL(response.url())
    if (!url.pathname.startsWith('/api/') && !url.pathname.startsWith('/auth/')) return
    if (response.status() >= 400 && !expected.some((pattern) => pattern.test(url.pathname))) {
      problems.push(`${response.request().method()} ${url.pathname}${url.search} -> ${response.status()}`)
    }
  })
  return problems
}

const heading = (page: Page) => page.locator('h1[data-page-title]').first()
const saveBar = (page: Page) => page.getByRole('region', { name: 'Save changes' })

async function open(page: Page, path: string) {
  await page.goto(path)
  await expect(heading(page)).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText(/Something went wrong|Unexpected Application Error|Cannot read properties/)).toHaveCount(0)
}

async function confirmIfAsked(page: Page) {
  const dialog = page.getByRole('alertdialog').or(page.getByRole('dialog'))
  if (await dialog.isVisible().catch(() => false)) await dialog.getByRole('button', { name: /Save|Apply|Continue|Restart/ }).first().click()
}

test.describe('live controller @live', () => {
  test('sign in with the admin token and return to the page asked for', async ({ page }) => {
    await page.goto(`/sites/${env.site}/aps`)
    await expect(page).toHaveURL(/\/login\?next=/)
    await page.getByLabel('Token', { exact: true }).fill(env.token)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(new RegExp(`/sites/${env.site}/aps`))
    await expect(heading(page)).toBeVisible()
  })

  test.describe('signed in', () => {
    test.beforeEach(async ({ page }) => signIn(page))

    test('every screen deep-links, renders and calls only working endpoints', async ({ page }) => {
      test.setTimeout(240_000)
      const [ap] = env.aps
      const problems = watch(page)
      const routes = [
        '/sites',
        `/sites/${env.site}/overview`,
        `/sites/${env.site}/aps`,
        `/sites/${env.site}/aps/${ap}`,
        `/sites/${env.site}/aps/${ap}/radios`,
        `/sites/${env.site}/aps/${ap}/clients`,
        `/sites/${env.site}/aps/${ap}/uplink`,
        `/sites/${env.site}/aps/${ap}/events`,
        `/sites/${env.site}/aps/${ap}/settings`,
        `/sites/${env.site}/aps/${ap}/config`,
        `/sites/${env.site}/clients`,
        `/sites/${env.site}/events`,
        `/sites/${env.site}/wlans`,
        `/sites/${env.site}/wlans/new`,
        `/sites/${env.site}/rf`,
        `/sites/${env.site}/rf/neighbours`,
        `/sites/${env.site}/settings/general`,
        `/sites/${env.site}/settings/access`,
        `/sites/${env.site}/settings/network`,
        `/sites/${env.site}/settings/ap-defaults`,
        `/sites/${env.site}/settings/protection`,
        `/sites/${env.site}/settings/advanced`,
        `/sites/${env.site}/changes`,
        '/system',
        '/system/backups',
        '/system/tokens',
        '/system/sessions',
        '/system/migration',
        '/onboard',
      ]
      for (const route of routes) {
        await open(page, route)
        await expectNoHorizontalScroll(page)
      }
      // A refresh keeps the deep link.
      await open(page, `/sites/${env.site}/settings/access`)
      const url = page.url()
      await page.reload()
      await expect(heading(page)).toBeVisible()
      expect(page.url()).toBe(url)
      expect(problems).toEqual([])
    })

    test('the AP shows real telemetry: log, neighbours, BLE, clients and history', async ({ page }) => {
      // A fresh controller has its first scan after about ten full frames.
      test.setTimeout(240_000)
      const [ap] = env.aps
      const problems = watch(page)
      // The AP log tab reads GET /api/aps/$id/log (fakeap sends a KMsg line).
      await open(page, `/sites/${env.site}/aps/${ap}/events?kind=log`)
      await expect(page.getByText('fakeap: synthetic kernel log line').first()).toBeVisible({ timeout: 20_000 })
      // Neighbours this AP's scanning radio heard, and only those.
      await open(page, `/sites/${env.site}/aps/${ap}/radios`)
      await expect(page.getByText('02:bb:00:00:00:01').locator('visible=true').first()).toBeVisible({ timeout: 90_000 })
      await expect(page.getByText('Alive')).toHaveCount(0) // BLE lives on the Overview tab
      // Utilisation is shown in percent (the API's 0.2 is 20 %), never 0 %.
      await expect(page.getByText(/Tx\s*10 %/).first()).toBeVisible()
      // The BLE indicator (field 38) on the Overview tab.
      await open(page, `/sites/${env.site}/aps/${ap}`)
      await expect(page.getByText('BLE radio')).toBeVisible({ timeout: 20_000 })
      await expect(page.getByText('Alive').first()).toBeVisible()
      // Site neighbours list both APs' scans.
      await open(page, `/sites/${env.site}/rf/neighbours`)
      await expect(page.getByText('Neighbour', { exact: true }).locator('visible=true').first()).toBeVisible({ timeout: 90_000 })
      // The neighbour's load is the API's 0.157 as a percentage.
      // (Load is a lower-priority column: wide tables only, from 1280 px.)
      if ((page.viewportSize()?.width ?? 0) >= 1280) await expect(page.getByText(/16 %/).locator('visible=true').first()).toBeVisible()
      // Clients and one client's detail; notes survive a reload.
      await open(page, `/sites/${env.site}/clients`)
      await page.locator('main a[href*="/clients/02-"]').locator('visible=true').first().click()
      await expect(heading(page)).toBeVisible()
      const notes = `Notes from ${test.info().project.name}`
      await page.getByRole('button', { name: /Name this client|Edit name/ }).click()
      await page.getByRole('textbox', { name: 'Name', exact: true }).fill(`Device ${test.info().project.name}`)
      await page.getByRole('textbox', { name: 'Notes' }).fill(notes)
      await page.getByRole('dialog').getByRole('button', { name: 'Save' }).click()
      await expect(page.getByRole('dialog')).toBeHidden()
      await page.reload()
      await expect(page.getByText(notes)).toBeVisible({ timeout: 15_000 })
      // The overview's client history has data (a line or, early on, dots).
      await open(page, `/sites/${env.site}/overview`)
      await expect(page.locator('figure svg path, figure svg circle[data-point]').first()).toBeAttached({ timeout: 90_000 })
      expect(problems).toEqual([])
    })

    test('register a pending AP', async ({ page }) => {
      test.skip(!env.pending || !env.pendingSecret, 'no pending fake AP')
      const pending = await (await page.request.get('/api/aps/pending')).json()
      const waiting = Array.isArray(pending) && pending.some((row: { id: string }) => row.id === env.pending)
      if (!waiting) {
        // An earlier project registered it: it is now an AP of the site.
        await open(page, `/sites/${env.site}/aps/${env.pending}`)
        return
      }
      await open(page, `/onboard?site=${env.site}`)
      await page.getByRole('link', { name: `Adopt ${env.pending}` }).first().click()
      await expect(page.getByRole('heading', { level: 1, name: 'Register an AP' })).toBeVisible()
      await expect(page.getByLabel('AP ID')).toHaveValue(env.pending)
      await page.getByRole('textbox', { name: 'Secret' }).fill(env.pendingSecret)
      await page.getByRole('button', { name: 'Register' }).click()
      await expect(page.getByRole('heading', { level: 1, name: 'Verify the AP' })).toBeVisible()
      const steps = page.getByRole('list', { name: 'Verification steps' })
      await expect(steps.getByText(/Authenticated/)).toBeVisible({ timeout: 30_000 })
      await expect(steps.getByText(/Config applied/)).toBeVisible({ timeout: 30_000 })
    })

    test('create a WLAN, edit it with the impact line, then RF and back after a dialog', async ({ page }) => {
      test.setTimeout(120_000)
      const problems = watch(page)
      const ssid = `Ex-${test.info().project.name}-${Date.now().toString(36).slice(-4)}`.slice(0, 32)
      await open(page, `/sites/${env.site}/wlans/new`)
      await page.getByLabel('SSID', { exact: true }).fill(ssid)
      await page.getByLabel(/^Passphrase/).first().fill('example-passphrase-9')
      await page.getByRole('button', { name: 'Create WLAN' }).click()
      await page.waitForURL(/\/wlans\/[0-9a-f-]{36}/, { timeout: 15_000 })
      // Edit: the save bar shows the dry run's impact line (§4.8).
      await page.getByRole('switch', { name: /Hidden SSID/ }).click()
      await expect(saveBar(page)).toContainText(/restarts on \d+ APs?|No AP changes|AP/, { timeout: 15_000 })
      await expect(saveBar(page)).not.toContainText(/restarts on (\d{2,}) APs/)
      await saveBar(page).getByRole('button', { name: 'Save' }).click()
      await confirmIfAsked(page)
      await expect(saveBar(page)).toBeHidden({ timeout: 15_000 })
      // Back after a dialog: the dialog closes and the page stays.
      const url = page.url()
      if ((page.viewportSize()?.width ?? 0) < 768) {
        await page.getByRole('button', { name: 'WLAN actions' }).click()
        await page.getByRole('menuitem', { name: 'Wi-Fi QR' }).click()
      } else await page.getByRole('button', { name: 'Wi-Fi QR' }).click()
      await expect(page.getByRole('dialog')).toBeVisible()
      await page.goBack()
      await expect(page.getByRole('dialog')).toBeHidden()
      expect(page.url()).toBe(url)
      // RF: a width change restarts the 5 GHz radio once per AP of the site
      // (the line counts APs, not diff paths).
      const aps = (await (await page.request.get(`/api/aps?site=${env.site}`)).json()) as unknown[]
      await open(page, `/sites/${env.site}/rf`)
      const width = page.getByRole('combobox', { name: '5 GHz width' })
      const current = await width.inputValue()
      await width.selectOption(current === '40' ? '80' : '40')
      await expect(saveBar(page)).toContainText(`5 GHz radio restarts on ${aps.length} AP${aps.length === 1 ? '' : 's'}`, { timeout: 15_000 })
      await saveBar(page).getByRole('button', { name: 'Save' }).click()
      await confirmIfAsked(page)
      await expect(saveBar(page)).toBeHidden({ timeout: 15_000 })
      expect(problems).toEqual([])
    })

    test('an AP override, a site rename, an AP default and restoring the revision before it', async ({ page }) => {
      test.setTimeout(150_000)
      const problems = watch(page)
      const ap = env.aps[1] ?? env.aps[0]
      const save = async () => {
        await saveBar(page).getByRole('button', { name: 'Save' }).click()
        await confirmIfAsked(page)
        await expect(saveBar(page)).toBeHidden({ timeout: 15_000 })
      }
      // AP settings: the first override switch is LED brightness.
      await open(page, `/sites/${env.site}/aps/${ap}/settings`)
      await page.getByRole('switch', { name: 'Override for this AP' }).first().click()
      await expect(saveBar(page)).toBeVisible({ timeout: 10_000 })
      await save()
      // Rename the site: dismist-only, no AP changes.
      await open(page, `/sites/${env.site}/settings/general`)
      const name = page.getByRole('textbox', { name: /^(Site )?name$/i }).first()
      const before = await name.inputValue()
      await name.fill(`Example ${test.info().project.name}`)
      await expect(saveBar(page)).toContainText(/No AP changes/, { timeout: 15_000 })
      await save()
      // A site AP default that restore covers.
      const site = async () => (await (await page.request.get(`/api/sites/${env.site}`)).json()) as { name: string; ap_defaults: { led_brightness: number } }
      const led = (await site()).ap_defaults.led_brightness
      await open(page, `/sites/${env.site}/settings/ap-defaults`)
      await page.getByRole('textbox', { name: 'LED brightness' }).fill(String(led === 255 ? 200 : 255))
      await expect(saveBar(page)).toContainText(/AP/, { timeout: 15_000 })
      await save()
      // Restore the revision before the LED change: LED comes back; the
      // name (dismist-only) is not part of a restore.
      const changes = (await (await page.request.get(`/api/sites/${env.site}/changes?limit=5`)).json()) as { changes: { revision: number; kind: string }[] }
      const configs = changes.changes.filter((change) => change.kind === 'config')
      expect(configs.length, 'the LED change and the rename are in the history').toBeGreaterThanOrEqual(2)
      const target = configs[1]
      await open(page, `/sites/${env.site}/changes/${target.revision}`)
      await page.getByRole('button', { name: 'Restore this revision' }).click()
      const restore = page.getByRole('button', { name: `Restore revision ${target.revision}` })
      await expect(restore).toBeEnabled({ timeout: 15_000 })
      await restore.click()
      await expect.poll(async () => (await site()).ap_defaults.led_brightness, { timeout: 15_000 }).toBe(led)
      // Put the name back for the next width.
      await open(page, `/sites/${env.site}/settings/general`)
      await page.getByRole('textbox', { name: /^(Site )?name$/i }).first().fill(before)
      await save()
      expect(problems).toEqual([])
    })

    test('events arrive live and page by time', async ({ page }) => {
      await open(page, `/sites/${env.site}/events`)
      const rows = page.getByRole('list', { name: 'Events' }).getByRole('listitem')
      await expect(rows.first()).toBeVisible({ timeout: 20_000 })
      const before = await rows.count()
      // fakeap sends a PACE event every 5 s; the stream upserts them.
      await expect.poll(async () => rows.count(), { timeout: 30_000 }).toBeGreaterThan(before)
      // Paging: events before the newest one's time, then back to the newest.
      const body = await (await page.request.get(`/api/sites/${env.site}/events?limit=2`)).json()
      const pivot = body.events[0].first_seen as string
      await page.goto(`/sites/${env.site}/events?before=${encodeURIComponent(pivot)}`)
      await expect(page.getByText(/Showing events before/)).toBeVisible()
      await page.getByRole('button', { name: 'Show the newest' }).click()
      await expect(page.getByText(/Showing events before/)).toBeHidden()
    })

    test('the stream recovers after a loss, and a session that expires during a stream asks to sign in again', async ({ page }) => {
      test.setTimeout(120_000)
      // Keep a handle on the page's EventSources, to drop them as a network
      // loss would.
      await page.addInitScript(() => {
        const sources: EventSource[] = []
        ;(window as unknown as { dismistSources: EventSource[] }).dismistSources = sources
        const Native = window.EventSource
        window.EventSource = class extends Native {
          constructor(url: string | URL, init?: EventSourceInit) {
            super(url, init)
            sources.push(this)
          }
        } as typeof EventSource
      })
      const drop = () =>
        page.evaluate(() => {
          for (const source of (window as unknown as { dismistSources: EventSource[] }).dismistSources) {
            if (source.readyState !== 2) {
              source.close()
              source.dispatchEvent(new Event('error'))
            }
          }
        })
      await open(page, `/sites/${env.site}/overview`)
      const status = page.locator('[role=status][data-status]').first()
      await expect(status).toHaveAttribute('data-status', 'live', { timeout: 20_000 })
      // Loss: the stream drops and reconnects fail until the network is back.
      await page.route('**/api/sites/*/stream', (route) => route.abort('internetdisconnected'))
      await drop()
      await expect(status).toHaveAttribute('data-status', /reconnecting|offline/, { timeout: 15_000 })
      await page.unroute('**/api/sites/*/stream')
      await page.evaluate(() => window.dispatchEvent(new Event('online')))
      await expect(status).toHaveAttribute('data-status', 'live', { timeout: 60_000 })
      // Session expiry during a stream: every session is revoked, the stream
      // drops, and the app asks to sign in and comes back here.
      const here = page.url()
      const revoked = await page.request.post('/api/system/sessions/revoke-all?include_current=1', { headers: { Origin: env.base } })
      expect(revoked.ok(), `revoke-all: ${revoked.status()}`).toBe(true)
      await drop()
      await expect(page).toHaveURL(/\/login\?next=/, { timeout: 30_000 })
      await page.getByLabel('Token', { exact: true }).fill(env.token)
      await page.getByRole('button', { name: 'Sign in' }).click()
      await expect(page).toHaveURL(here)
    })
  })
})
