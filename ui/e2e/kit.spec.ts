import { checkPage, expect, expectAccessible, isPhone, ready, test } from './harness'

// The shared components and forms kit at every width (design §7.7, §7.8):
// the 16 px rule, tap targets, axe and no sideways scroll with every section
// open, then the behaviours the screens rely on.

test.describe('component kit', () => {
  test.beforeEach(async ({ signedIn: page }) => {
    await page.goto('/next/dev/kit')
    await ready(page)
    await expect(page.getByRole('heading', { level: 1, name: 'Component kit' })).toBeVisible()
  })

  test('every section open passes the layout rules', async ({ signedIn: page }) => {
    for (const name of ['Numbers', 'Lists']) {
      const trigger = page.getByRole('button', { name: new RegExp(`^${name}`) })
      if ((await trigger.getAttribute('aria-expanded')) === 'false') await trigger.click()
    }
    await expect(page.getByLabel('Minimum client signal')).toBeVisible()
    await expect(page.locator('[data-json-editor] .cm-content')).toBeVisible()
    await checkPage(page)
  })

  test('negative-only numbers type the magnitude behind a fixed minus', async ({ signedIn: page }) => {
    const field = page.getByLabel('Minimum client signal')
    await expect(field).toHaveAttribute('inputmode', 'numeric')
    await field.fill('72')
    await field.blur()
    await expect(field).toHaveValue('72')
    const bar = page.getByRole('region', { name: 'Unsaved changes' })
    await expect(bar).toContainText('1 change')
    await expect(bar).toContainText('Applies to 2 APs · WLAN restarts')
    // A value outside −95 to −50 is an inline error.
    await field.fill('40')
    await field.blur()
    await expect(page.getByText('Use −95 to −50 dBm.')).toBeVisible()
  })

  test('the save bar sits above the tab bar, and above the keyboard while it is open', async ({ signedIn: page }) => {
    test.skip(!isPhone(page), 'phones only')
    const ssid = page.getByLabel('Network name (SSID)')
    await ssid.fill('Example-2')
    const bar = page.locator('[data-action-bar]')
    const tabs = page.getByRole('navigation', { name: 'Tabs' })
    await expect(bar).toBeVisible()
    await ssid.blur()
    const barBox = (await bar.boundingBox())!
    const tabsBox = (await tabs.boundingBox())!
    expect(barBox.y + barBox.height).toBeLessThanOrEqual(tabsBox.y + 1)

    // Emulate the on-screen keyboard: half the viewport, a focused field.
    await ssid.focus()
    await page.evaluate(() => {
      Object.defineProperty(window.visualViewport!, 'height', { configurable: true, get: () => window.innerHeight * 0.5 })
      window.visualViewport!.dispatchEvent(new Event('resize'))
    })
    await expect(tabs).toBeHidden()
    const visible = await page.evaluate(() => window.innerHeight * 0.5)
    await expect.poll(async () => Math.round((await bar.boundingBox())!.y + (await bar.boundingBox())!.height)).toBeLessThanOrEqual(Math.round(visible) + 1)
  })

  test('a server field error lands on its field', async ({ signedIn: page }) => {
    await page.getByLabel('Network name (SSID)').fill('taken')
    await page.locator('[data-action-bar]').getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Example-Guest already uses that name.')).toBeVisible()
    await expect(page.getByLabel('Network name (SSID)')).toHaveAttribute('aria-invalid', 'true')
  })

  test('View diff shows per-AP redacted changes', async ({ signedIn: page }) => {
    await page.getByRole('switch', { name: 'Hidden SSID' }).click()
    const bar = page.locator('[data-action-bar]')
    await bar.getByRole('button', { name: 'View diff' }).click()
    const dialog = page.getByRole('dialog', { name: 'Impact of saving' })
    await expect(dialog).toContainText('Applies to 2 APs')
    await dialog.getByRole('button', { name: /Hallway/ }).click()
    await expect(dialog.getByText('changed (secret, not shown)').first()).toBeVisible()
    await checkPage(page)
  })

  test('leaving with unsaved changes asks first', async ({ signedIn: page }) => {
    await page.getByRole('textbox', { name: 'Name', exact: true }).fill('Renamed')
    if (isPhone(page)) await page.getByRole('navigation', { name: 'Tabs' }).getByRole('link', { name: 'Overview' }).click()
    else await page.getByRole('link', { name: 'Dismist home' }).click()
    const dialog = page.getByRole('alertdialog', { name: 'Discard changes?' })
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Keep editing' }).click()
    await expect(page).toHaveURL(/\/next\/dev\/kit/)
    await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Renamed')
  })

  test('type-to-confirm guards a delete', async ({ signedIn: page }) => {
    await page.getByRole('button', { name: 'Actions for Hallway' }).first().click()
    await page.getByRole(isPhone(page) || (await page.evaluate(() => matchMedia('(pointer: coarse)').matches)) ? 'button' : 'menuitem', { name: 'Delete' }).click()
    const dialog = page.getByRole('alertdialog', { name: 'Delete Hallway?' })
    const confirm = dialog.getByRole('button', { name: 'Delete AP' })
    await expect(confirm).toBeDisabled()
    await dialog.getByLabel(/Type Hallway to confirm/).fill('Hallway')
    await expect(confirm).toBeEnabled()
    await confirm.click()
    await expect(dialog).toBeHidden()
  })

  test('the expert editor flags a forbidden key', async ({ signedIn: page }) => {
    await expect(page.locator('.cm-lint-marker-error').first()).toBeVisible({ timeout: 5000 })
  })
})

test.describe('keyboard shortcuts', () => {
  test('g then a goes to the site’s APs; ? lists the shortcuts', async ({ signedIn: page }) => {
    test.skip(isPhone(page), 'desktop only')
    await page.goto('/next/sites/home/overview')
    await ready(page)
    await page.locator('body').click({ position: { x: 1, y: 1 } })
    await page.keyboard.press('g')
    await page.keyboard.press('a')
    await expect(page).toHaveURL(/\/next\/sites\/home\/aps$/)
    await page.keyboard.press('?')
    await expect(page.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeVisible()
  })
})

test.describe('dark theme', () => {
  test('the kit and the sign-in page pass axe in dark mode', async ({ signedIn: page }) => {
    await page.addInitScript(() => localStorage.setItem('dismist.theme', 'dark'))
    await page.goto('/next/dev/kit')
    await ready(page)
    await expect(page.locator('html')).toHaveClass(/dark/)
    await expectAccessible(page)
    await page.evaluate(() => window.__dismistMock!.signOut())
    await page.goto('/next/login')
    await ready(page)
    await expectAccessible(page)
  })
})
