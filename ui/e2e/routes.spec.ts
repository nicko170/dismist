import type { Page } from '@playwright/test'
import { checkPage, expect, ready, test } from './harness'
import { routes, type RouteCase } from './routes'

async function visit(page: Page, route: RouteCase) {
  await page.goto(route.path)
  await ready(page)
  await expect(page.getByRole('heading', { level: 1, name: route.heading })).toBeVisible()
  await checkPage(page)
  // Refresh lands on the same screen.
  await page.reload()
  await ready(page)
  await expect(page.getByRole('heading', { level: 1, name: route.heading })).toBeVisible()
}

for (const route of routes) {
  if (route.signedIn) {
    test(`${route.path} renders and passes the layout rules`, async ({ signedIn }) => visit(signedIn, route))
  } else {
    test(`${route.path} renders and passes the layout rules`, async ({ page }) => visit(page, route))
  }
}
