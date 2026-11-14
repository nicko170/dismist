import type { Page } from '@playwright/test'
import { checkPage, expect, ready, test } from './harness'

// Onboarding screens (design §3.15, §7.10): every route at every width,
// refresh and deep links (including ?step=verify), and registering an AP
// from the pending list. Runs against the MSW mock build; synthetic data.

const routes = [
  { path: '/next/onboard', heading: 'Add an AP' },
  { path: '/next/onboard?site=home', heading: 'Add an AP' },
  { path: '/next/onboard/register?site=home', heading: 'Register an AP' },
  { path: '/next/onboard/register?step=verify&id=02-aa-00-00-00-01&site=home', heading: 'Verify the AP' },
]

async function visit(page: Page, path: string, heading: string) {
  await page.goto(path)
  await ready(page)
  await expect(page.getByRole('heading', { level: 1, name: heading })).toBeVisible()
}

for (const route of routes) {
  test(`${route.path} renders, passes the layout rules and survives a refresh`, async ({ signedIn: page }) => {
    await visit(page, route.path, route.heading)
    // Let the data regions settle before the layout and axe checks.
    await page.waitForLoadState('networkidle')
    await checkPage(page)
    await page.reload()
    await ready(page)
    await expect(page.getByRole('heading', { level: 1, name: route.heading })).toBeVisible()
  })
}

test('the hub lists the waiting AP and builds the CLI command', async ({ signedIn: page }) => {
  await visit(page, '/next/onboard?site=home', 'Add an AP')
  await expect(page.getByText('Not registered').filter({ visible: true }).first()).toBeVisible()
  await page.getByLabel('AP name').fill('Front room')
  await page.getByLabel('AP LAN address').fill('192.0.2.10')
  await expect(page.getByLabel('Onboard command')).toContainText(
    "dismist ap33 onboard root@192.0.2.10 --controller http://127.0.0.1:",
  )
  await expect(page.getByLabel('Onboard command')).toContainText("--site Home --name 'Front room'")
  await expect(page.getByLabel('cloud.ini')).toBeVisible()
})

test('registering from the pending list lands on the live Verify panel', async ({ signedIn: page }) => {
  await visit(page, '/next/onboard?site=home', 'Add an AP')
  await page.getByRole('link', { name: 'Adopt 02-aa-00-00-00-20' }).filter({ visible: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Register an AP' })).toBeVisible()
  await expect(page.getByLabel('AP ID')).toHaveValue('02-aa-00-00-00-20')
  await page.getByLabel('Name').fill('Garage')
  await page.getByLabel('Secret', { exact: true }).fill('a0'.repeat(32))
  await page.getByRole('button', { name: 'Register' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Verify the AP' })).toBeVisible()
  await expect(page).toHaveURL(/\/next\/onboard\/register\?.*step=verify/)
  await expect(page).toHaveURL(/id=02-aa-00-00-00-20/)
  // The mock's POST copies another AP's session fields, so only the first
  // step is predictable here (the full sequence is a Vitest flow test).
  await expect(page.getByRole('list', { name: 'Verification steps' })).toContainText('Registered as Garage')
  // Back does not return to the filled-in form (the verify step replaced it).
  await page.goBack()
  await expect(page.getByRole('heading', { level: 1, name: 'Add an AP' })).toBeVisible()
})

test('the Verify panel of a connected AP shows it authenticated and in sync', async ({ signedIn: page }) => {
  await visit(page, '/next/onboard/register?step=verify&id=02-aa-00-00-00-01', 'Verify the AP')
  const steps = page.getByRole('list', { name: 'Verification steps' })
  await expect(steps).toContainText('Authenticated (AP33-WW, apfw-0.15.35064-noro-84df)')
  await expect(steps).toContainText('Config applied (rev 42)')
})
