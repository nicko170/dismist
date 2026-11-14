import type { Page } from '@playwright/test'
import { checkPage, expect, ready, test } from './harness'

// Workstream F's configuration routes (design §3.6 to §3.11, §3.13): every
// screen renders its H1, passes the layout rules (no sideways scroll, 16 px
// inputs on touch and phones, 44 px targets, axe) at every tested width, and a
// refresh lands on the same screen. Runs against the MSW mock build.

type ConfigRoute = { path: string; heading: string | RegExp; expand?: boolean }

const AP = '02-aa-00-00-00-01'

export const configRoutes: ConfigRoute[] = [
  { path: '/next/sites/home/wlans', heading: 'WLANs' },
  { path: '/next/sites/home/wlans/new', heading: 'New WLAN', expand: true },
  { path: '/next/sites/home/wlans/new?preset=iot', heading: 'New WLAN' },
  { path: '/next/sites/home/wlans/wlan-example', heading: 'Example', expand: true },
  { path: '/next/sites/home/wlans/wlan-example/expert', heading: 'Expert patch · WLAN Example' },
  { path: '/next/sites/lab/wlans/wlan-lab', heading: 'Example-LAB', expand: true },
  { path: '/next/sites/home/rf', heading: 'Radio settings', expand: true },
  { path: '/next/sites/home/rf/neighbours', heading: 'Neighbours' },
  { path: '/next/sites/home/settings/general', heading: 'Settings' },
  { path: '/next/sites/home/settings/access', heading: 'Settings' },
  { path: '/next/sites/home/settings/network', heading: 'Settings' },
  { path: '/next/sites/home/settings/ap-defaults', heading: 'Settings' },
  { path: '/next/sites/home/settings/protection', heading: 'Settings' },
  { path: '/next/sites/home/settings/advanced', heading: 'Settings' },
  { path: '/next/sites/home/settings/danger', heading: 'Settings' },
  { path: '/next/sites/lab/settings/ap-defaults', heading: 'Settings' },
  { path: '/next/sites/home/changes', heading: 'Changes' },
  { path: '/next/sites/home/changes/47', heading: 'Change 47' },
  { path: `/next/sites/home/aps/${AP}/settings`, heading: 'Hallway settings', expand: true },
  { path: `/next/sites/home/aps/${AP}/config`, heading: 'Hallway config' },
  { path: `/next/sites/home/aps/${AP}/config?view=expert`, heading: 'Hallway config' },
  { path: `/next/sites/home/aps/${AP}/config?view=history`, heading: 'Hallway config' },
  { path: '/next/sites/lab/aps/02-aa-00-00-00-10/settings', heading: 'Lab bench settings', expand: true },
]

/** Opens every collapsed section so the checks cover the advanced fields too. */
async function expandSections(page: Page) {
  const collapsed = page.locator('main section > h2 > button[aria-expanded="false"]')
  for (let guard = 0; guard < 30 && (await collapsed.count()) > 0; guard++) await collapsed.first().click()
}

async function visit(page: Page, route: ConfigRoute) {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.goto(route.path)
  await ready(page)
  await expect(page.getByRole('heading', { level: 1, name: route.heading, exact: typeof route.heading === 'string' })).toBeVisible()
  if (route.expand) await expandSections(page)
  await checkPage(page)
  await page.reload()
  await ready(page)
  await expect(page.getByRole('heading', { level: 1, name: route.heading, exact: typeof route.heading === 'string' })).toBeVisible()
  expect(errors, 'uncaught page errors').toEqual([])
}

for (const route of configRoutes) {
  test(`${route.path} renders and passes the layout rules`, async ({ signedIn }) => visit(signedIn, route))
}

test('/sites/$siteId/settings redirects to general', async ({ signedIn: page }) => {
  await page.goto('/next/sites/home/settings')
  await expect(page).toHaveURL(/\/next\/sites\/home\/settings\/general$/)
})
