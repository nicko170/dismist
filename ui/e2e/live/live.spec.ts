import { readFileSync } from 'node:fs'
import { checkPage, expect, ready, test } from '../harness'
import { liveState, liveStorage, type LiveState } from './global-setup'

// Against dismistd and fakeap (E2E_LIVE=1, design §7.10). The UI is the
// production build embedded by go build, served at /next/ in phases 0 and
// 1 and at / from M1 (where /next/* redirects), so paths are matched with
// an optional /next prefix.

const state = (): LiveState => JSON.parse(readFileSync(liveState, 'utf8')) as LiveState

test.describe('live controller', () => {
  test('sign in with the admin token and land on next', async ({ page }) => {
    const { siteId, adminToken } = state()
    await page.goto(`/next/sites/${siteId}/aps`)
    await expect(page).toHaveURL(/\/login\?next=/)
    await page.getByLabel('Token', { exact: true }).fill(adminToken)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(new RegExp(`(/next)?/sites/${siteId}/aps`))
  })

  test.describe('signed in', () => {
    test.use({ storageState: liveStorage })

    test('the shell shows the site, a live stream and the fake AP online', async ({ page }) => {
      const { siteId } = state()
      await page.goto(`/next/sites/${siteId}/overview`)
      await ready(page)
      await expect(page.locator('[role=status][data-status="live"]').first()).toBeAttached({ timeout: 15_000 })
      // No offline badge and no "(n)" prefix while the AP is connected.
      await expect(page).toHaveTitle(/Home · Dismist$/)
      await expect(page).not.toHaveTitle(/^\(\d+\)/)
      await checkPage(page)
    })

    test('a deep link survives a refresh', async ({ page }) => {
      const { siteId } = state()
      await page.goto(`/next/sites/${siteId}/settings`)
      await ready(page)
      const url = page.url()
      await page.reload()
      await ready(page)
      expect(page.url()).toBe(url)
    })
  })
})
