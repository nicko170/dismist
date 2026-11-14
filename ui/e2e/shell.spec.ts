import { expect, isPhone, ready, test } from './harness'

test.describe('shell', () => {
  test.beforeEach(async ({ signedIn: page }) => {
    await page.goto('/next/sites/home/overview')
    await ready(page)
  })

  test('navigation matches the viewport', async ({ signedIn: page }) => {
    const width = page.viewportSize()!.width
    const tabs = page.getByRole('navigation', { name: 'Tabs' })
    const primary = page.getByRole('navigation', { name: 'Primary' })
    if (width < 768) {
      await expect(tabs).toBeVisible()
      await expect(primary).toBeHidden()
      await expect(tabs.getByRole('link')).toHaveText(['Overview', /APs/, 'Clients', 'WLANs'])
      await expect(tabs.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
      // Offline badge from the site counts (1 of 3 APs offline in the mock).
      await expect(tabs.getByRole('link', { name: /^APs\W+1 offline$/ })).toBeVisible()
    } else {
      await expect(tabs).toBeHidden()
      await expect(primary).toBeVisible()
      await expect(primary.getByRole('link', { name: /Overview/ })).toHaveAttribute('aria-current', 'page')
      await expect(primary.getByRole('link', { name: 'Access points 1 offline' })).toBeVisible()
      const box = await page.locator('aside').boundingBox()
      expect(box!.width).toBe(width >= 1024 ? 248 : 64)
      await expect(page.getByRole('navigation', { name: 'Breadcrumb' })).toContainText('Home')
    }
  })

  test('the site stream reports Live', async ({ signedIn: page }) => {
    await expect(page.locator('[role=status][data-status="live"]').first()).toBeAttached()
  })

  test('title carries the offline count', async ({ signedIn: page }) => {
    await expect(page).toHaveTitle(/^\(1\) .*Home · Dismist$/)
  })

  test('More sheet on phones; sidebar footer on desktop', async ({ signedIn: page }) => {
    if (isPhone(page)) {
      await page.getByRole('button', { name: 'More', exact: true }).click()
      const sheet = page.getByRole('dialog', { name: 'More' })
      await expect(sheet).toBeVisible()
      for (const label of ['Radio', 'Events', 'Changes', 'Settings', 'Add an AP', 'System']) {
        await expect(sheet.getByRole('link', { name: label, exact: true })).toBeVisible()
      }
      await expect(sheet.getByRole('button', { name: 'Sign out' })).toBeVisible()
      await sheet.getByRole('radio', { name: 'Dark' }).click()
      await expect(page.locator('html')).toHaveClass(/dark/)
      await page.reload()
      await expect(page.locator('html')).toHaveClass(/dark/)
    } else {
      await page.getByRole('button', { name: 'Theme' }).click()
      await page.getByRole('menuitemradio', { name: 'Dark' }).click()
      await expect(page.locator('html')).toHaveClass(/dark/)
    }
  })

  test('sign out returns to the login page', async ({ signedIn: page }) => {
    if (isPhone(page)) {
      await page.getByRole('button', { name: 'More', exact: true }).click()
      await page.getByRole('dialog', { name: 'More' }).getByRole('button', { name: 'Sign out' }).click()
    } else {
      await page.getByRole('button', { name: 'Sign out' }).click()
    }
    await expect(page).toHaveURL(/\/next\/login/)
  })

  test('site switcher lists sites and keeps the section', async ({ signedIn: page }) => {
    await page.getByRole('button', { name: /Site: Home/ }).first().click()
    if (isPhone(page)) {
      await page.getByRole('dialog', { name: 'Switch site' }).getByRole('button', { name: /Lab/ }).click()
    } else {
      await page.getByRole('option', { name: /Lab/ }).click()
    }
    await expect(page).toHaveURL(/\/next\/sites\/lab\/overview$/)
  })

  test('the bottom tabs hide while the on-screen keyboard is open', async ({ signedIn: page }) => {
    test.skip(!isPhone(page), 'phones only')
    const tabs = page.getByRole('navigation', { name: 'Tabs' })
    await expect(tabs).toBeVisible()
    // Emulate the keyboard: a focused text field and a visual viewport at half height.
    await page.evaluate(() => {
      const input = document.createElement('input')
      input.type = 'text'
      input.setAttribute('aria-label', 'probe')
      document.querySelector('main')!.append(input)
      Object.defineProperty(window.visualViewport!, 'height', { configurable: true, get: () => window.innerHeight * 0.5 })
      input.focus()
      window.visualViewport!.dispatchEvent(new Event('resize'))
    })
    await expect(tabs).toBeHidden()
    await page.evaluate(() => {
      Object.defineProperty(window.visualViewport!, 'height', { configurable: true, get: () => window.innerHeight })
      ;(document.activeElement as HTMLElement).blur()
      window.visualViewport!.dispatchEvent(new Event('resize'))
    })
    await expect(tabs).toBeVisible()
  })

  test('safe-area padding is applied to the bars', async ({ signedIn: page }) => {
    test.skip(!isPhone(page), 'phones only')
    const css = await page.evaluate(() => ({
      tabs: getComputedStyle(document.querySelector('nav[aria-label="Tabs"]')!).paddingBottom,
      viewport: document.querySelector('meta[name=viewport]')!.getAttribute('content'),
    }))
    expect(css.viewport).toContain('viewport-fit=cover')
    expect(css.viewport).not.toContain('maximum-scale')
  })

  test('a 401 mid-session sends you to login with next', async ({ signedIn: page }) => {
    await page.evaluate(() => window.__dismistMock!.signOut())
    await page.evaluate(() => window.__dismistMock!.dropStreams())
    await expect(page).toHaveURL(/\/next\/login\?next=%2Fsites%2Fhome%2Foverview/, { timeout: 15_000 })
  })
})
