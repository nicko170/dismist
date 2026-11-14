import type { Page } from '@playwright/test'
import { checkPage, ready } from '../harness'
import { expect, test } from './fixture'

// Every workstream-E route at every width (design §7.7, §7.8, §7.10): the
// screen renders, passes the layout rules (no sideways scroll, 16 px
// controls, 44 px targets below md) and axe, and a refresh lands on the
// same screen. Kept here rather than in e2e/routes.ts so parallel
// workstreams do not conflict on one file.

type Case = { path: string; heading: string | RegExp }

const AP = '02-aa-00-00-00-01'
const cases: Case[] = [
  { path: '/next/sites', heading: 'Sites' },
  { path: '/next/sites/home/overview', heading: 'Home' },
  { path: '/next/sites/home/overview?range=7d', heading: 'Home' },
  { path: '/next/sites/home/aps', heading: 'Access points' },
  { path: '/next/sites/home/aps?status=offline&sort=status', heading: 'Access points' },
  { path: `/next/sites/home/aps/${AP}`, heading: 'Hallway' },
  { path: `/next/sites/home/aps/${AP}/radios`, heading: 'Hallway' },
  { path: `/next/sites/home/aps/${AP}/clients`, heading: 'Hallway' },
  { path: `/next/sites/home/aps/${AP}/uplink`, heading: 'Hallway' },
  { path: `/next/sites/home/aps/${AP}/events`, heading: 'Hallway' },
  { path: `/next/sites/home/aps/${AP}/events?kind=log`, heading: 'Hallway' },
  { path: '/next/sites/home/aps/02-aa-00-00-00-02', heading: 'Garage' },
  { path: '/next/sites/home/clients', heading: 'Clients' },
  { path: '/next/sites/home/clients?view=recent', heading: 'Clients' },
  { path: '/next/sites/home/clients/02-aa-bb-00-00-01', heading: 'phone' },
  { path: '/next/sites/home/events', heading: 'Events' },
  { path: '/next/sites/home/events?kind=dhcp_failure&range=24h', heading: 'Events' },
  { path: '/next/system', heading: 'System' },
  { path: '/next/system/backups', heading: 'System' },
  { path: '/next/system/tokens', heading: 'System' },
  { path: '/next/system/sessions', heading: 'System' },
  { path: '/next/system/migration', heading: 'System' },
]

async function visit(page: Page, route: Case) {
  await page.goto(route.path)
  await ready(page)
  await expect(page.getByRole('heading', { level: 1, name: route.heading, exact: typeof route.heading === 'string' })).toBeVisible()
  await checkPage(page)
  await page.reload()
  await ready(page)
  await expect(page.getByRole('heading', { level: 1, name: route.heading, exact: typeof route.heading === 'string' })).toBeVisible()
}

for (const route of cases) {
  test(`${route.path} renders and passes the layout rules`, async ({ signedIn }) => visit(signedIn, route))
}

test('the New site dialog has its own URL and survives a refresh', async ({ signedIn: page }) => {
  await page.goto('/next/sites?dialog=new-site')
  await ready(page)
  const dialog = page.getByRole('dialog', { name: 'New site' })
  await expect(dialog).toBeVisible()
  await checkPage(page)
  await page.reload()
  await ready(page)
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Close' }).click()
  await expect(page).toHaveURL(/\/next\/sites$/)
})

test('stable AP and client links redirect to their site', async ({ signedIn: page }) => {
  await page.goto(`/next/aps/${AP}`)
  await expect(page).toHaveURL(new RegExp(`/next/sites/home/aps/${AP}$`))
  await expect(page.getByRole('heading', { level: 1, name: 'Hallway' })).toBeVisible()
  await page.goto('/next/clients/02:AA:BB:00:00:01')
  await expect(page).toHaveURL(/\/next\/sites\/home\/clients\/02-aa-bb-00-00-01$/)
  await expect(page.getByRole('heading', { level: 1, name: 'phone' })).toBeVisible()
})

test('unknown AP and client show not found', async ({ signedIn: page }) => {
  await page.goto('/next/sites/home/aps/02-aa-00-00-99-99')
  await expect(page.getByRole('heading', { level: 1, name: 'Access point not found' })).toBeVisible()
  await page.goto('/next/clients/02-aa-bb-00-99-99')
  await expect(page.getByRole('heading', { level: 1, name: 'Client not found' })).toBeVisible()
})
