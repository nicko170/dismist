import type { Page } from '@playwright/test'
import { expect, isPhone, ready, test } from './harness'

// End-to-end flows for the configuration screens (design §3.0 "Saving", §3.6
// to §3.11, §3.13, §7.10) against the MSW mocks: impact line from the dry
// run, create and edit, passphrase rules, experimental gating, the unsaved
// guard, 409 stale, RF width re-mapping, SSH keys, AP overrides, protocol
// features, restore with secrets, legacy mode and expert-patch checks.

const AP = '02-aa-00-00-00-01'

async function open(page: Page, path: string) {
  await page.goto(path)
  await ready(page)
}

function saveBar(page: Page) {
  return page.getByRole('region', { name: 'Save changes' })
}

async function expandSection(page: Page, name: string | RegExp) {
  const trigger = page.locator('main section > h2 > button', { hasText: name }).first()
  if ((await trigger.getAttribute('aria-expanded')) === 'false') await trigger.click()
}

test.describe('WLAN editor', () => {
  test('an edit shows the impact line, then saves', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans/wlan-example')
    await page.getByRole('switch', { name: 'Hidden SSID', exact: true }).click()
    const bar = saveBar(page)
    await expect(bar).toContainText('1 change')
    await expect(bar).toContainText(/WLAN Example restarts on 3 APs; about \d+ clients reconnect/)
    await bar.getByRole('button', { name: 'View diff' }).click()
    await expect(page.getByRole('dialog')).toContainText('WLANs[Example].hidden')
    await page.keyboard.press('Escape')
    await bar.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('WLAN Example saved')).toBeVisible()
    await expect(saveBar(page)).toHaveCount(0)
  })

  test('creates a WLAN with byte-accurate SSID and passphrase checks', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans/new?preset=home')
    const ssid = page.getByLabel('SSID', { exact: true })
    await ssid.fill('Café guest')
    await expect(page.getByText('11/32 bytes')).toBeVisible()
    const psk = page.getByLabel('Passphrase', { exact: true })
    await psk.fill('short')
    await psk.blur()
    await expect(page.getByText('At least 8 characters.')).toBeVisible()
    await psk.fill('a good passphrase')
    await expect(page.getByText('At least 8 characters.')).toHaveCount(0)
    await saveBar(page).getByRole('button', { name: 'Create WLAN' }).click()
    await expect(page).toHaveURL(/\/next\/sites\/home\/wlans\/wlan-[0-9a-f]+$/)
    await expect(page.getByRole('heading', { level: 1, name: 'Café guest' })).toBeVisible()
  })

  test('WPA3 refuses a 64-hex key; WPA2 accepts it', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans/new')
    await page.getByLabel('SSID', { exact: true }).fill('Hex test')
    await page.getByRole('radio', { name: /^WPA3-Personal/ }).click()
    const psk = page.getByLabel('Passphrase', { exact: true })
    await psk.fill('ab'.repeat(32))
    await psk.blur()
    await expect(page.getByText(/a 64-digit hex key only works with WPA2-Personal/)).toBeVisible()
    await page.getByRole('radio', { name: /^WPA2-Personal/ }).click()
    await expect(page.getByText(/a 64-digit hex key only works with WPA2-Personal/)).toHaveCount(0)
  })

  test('experimental security values appear only on experimental sites', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans/new')
    await expect(page.getByRole('radio', { name: /^WPA2\/WPA3-Personal/ })).toBeVisible()
    await expect(page.getByRole('radio', { name: /Enhanced Open/ })).toHaveCount(0)
    await open(page, '/next/sites/lab/wlans/new')
    await expect(page.getByRole('radio', { name: /^Enhanced Open \(OWE\).*Experimental/ })).toBeVisible()
  })

  test('the unsaved-changes guard stops Back until the edit is discarded', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans')
    await page.getByRole('link', { name: 'Example', exact: true }).first().click()
    await expect(page.getByRole('heading', { level: 1, name: 'Example' })).toBeVisible()
    await page.getByLabel('SSID', { exact: true }).fill('Example 2')
    await page.goBack()
    const dialog = page.getByRole('alertdialog', { name: 'Discard changes?' })
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Keep editing' }).click()
    await expect(page.getByLabel('SSID', { exact: true })).toHaveValue('Example 2')
    await page.goBack()
    await page.getByRole('alertdialog', { name: 'Discard changes?' }).getByRole('button', { name: 'Discard changes' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'WLANs' })).toBeVisible()
  })

  test('a save over someone else’s change offers Reload or Overwrite', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans/wlan-example')
    await page.evaluate(async () => {
      await fetch('/api/sites/home/networks/wlan-example', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dtim: 3 }),
      })
    })
    await page.getByRole('switch', { name: 'Hidden SSID', exact: true }).click()
    await saveBar(page).getByRole('button', { name: 'Save' }).click()
    const dialog = page.getByRole('alertdialog', { name: 'This changed since you opened it' })
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Overwrite' }).click()
    await expect(page.getByText('WLAN Example saved')).toBeVisible()
  })

  test('the expert patch refuses forbidden paths as you type', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans/wlan-example/expert')
    const editor = page.locator('.cm-content')
    await editor.click()
    await editor.fill('{"ssid": "Other"}')
    await expect(page.getByText(/SSID is set by the WLAN form/)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Save patch' })).toBeDisabled()
    await editor.fill('{"DTIM": {"Period": 3}}')
    await expect(page.getByText(/SSID is set by the WLAN form/)).toHaveCount(0)
    await expect(page.getByRole('region', { name: 'Save expert patch' })).toContainText(/restarts on 3 APs/)
  })
})

test.describe('legacy mode', () => {
  test.beforeEach(async ({ signedIn: page }) => {
    await page.addInitScript(() => localStorage.setItem('dismist.mock.migration', 'legacy'))
  })

  test('typed fields are read-only; SSID and Enabled still edit', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans/wlan-example')
    await expect(page.getByText('Typed settings are paused: dismist is running your existing config unchanged.')).toBeVisible()
    await expect(page.getByRole('switch', { name: 'Hidden SSID', exact: true })).toBeDisabled()
    await expect(page.getByLabel('SSID', { exact: true })).toBeEnabled()
    await expect(page.getByRole('switch', { name: 'Enabled', exact: true })).toBeEnabled()
  })
})

test.describe('radio settings', () => {
  test('a width change re-maps the Auto channels and previews a radio restart', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/rf')
    const band5 = page.getByRole('region', { name: '5 GHz' })
    await expect(band5.getByRole('button', { name: /^36–48/ })).toHaveAttribute('aria-pressed', 'true')
    await band5.getByLabel('5 GHz width').selectOption('40')
    await expect(band5.getByRole('button', { name: /^36–40/ })).toHaveAttribute('aria-pressed', 'true')
    await expect(band5.getByRole('button', { name: /^149–153/ })).toHaveAttribute('aria-pressed', 'true')
    await expect(band5.getByRole('button', { name: /^52–56/ })).toBeDisabled()
    await expect(band5.getByRole('option', { name: '160 MHz' })).toHaveCount(0)
    const bar = saveBar(page)
    await expect(bar).toContainText(/radio restarts on 3 APs/)
    await bar.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Radio settings saved')).toBeVisible()
  })

  test('Plan channels previews, then applies', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/rf')
    await page.getByRole('button', { name: 'Plan channels' }).click()
    const dialog = page.getByRole('dialog', { name: 'Plan channels' })
    await expect(dialog).toContainText('Keep')
    await expect(dialog).toContainText(/Radio restarts/)
    await dialog.getByRole('button', { name: 'Apply plan' }).click()
    await expect(page.getByText('Channel plan applied')).toBeVisible()
  })
})

test.describe('site settings', () => {
  test('SSH keys are pasted, fingerprinted and saved; private keys are refused', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/settings/access')
    const paste = page.getByLabel('Paste public keys')
    await paste.fill('-----BEGIN OPENSSH PRIVATE KEY-----')
    await page.getByRole('button', { name: 'Add keys' }).click()
    await expect(page.getByText(/This is a private key/)).toBeVisible()
    await paste.fill('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEUEUFgIbAddz8QpHk7ULufpCRX9Obj5krYposbKm/YQ admin@example')
    await page.getByRole('button', { name: 'Add keys' }).click()
    await expect(page.getByText('admin@example')).toBeVisible()
    await expect(page.getByText('SHA256:dEeUqHbpsbgeYPEMeQO6MlIJYO8jlEXMkvk8icjkrMg')).toBeVisible()
    await saveBar(page).getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Site settings saved')).toBeVisible()
  })

  test('a recommendation is previewed before it is applied', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/settings/advanced')
    await page.getByRole('listitem').filter({ hasText: 'Keep radios on when PoE is short' }).getByRole('button', { name: 'Preview' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('PoePwrConstrained.EnableRadios')
    await dialog.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText('Recommendation applied')).toBeVisible()
  })

  test('a site with APs cannot be deleted', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/settings/danger')
    await expect(page.getByText('Move or delete its 3 APs first.')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Delete Home' })).toBeDisabled()
  })

  test('renaming the site saves without touching APs', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/settings/general')
    await page.getByLabel('Name', { exact: true }).fill('Home 2')
    await saveBar(page).getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Site settings saved')).toBeVisible()
    await expect(page.getByText('No AP changes')).toBeVisible()
  })
})

test.describe('AP settings', () => {
  test('a static channel lists AU/890 primaries with DFS locked', async ({ signedIn: page }) => {
    await open(page, `/next/sites/home/aps/${AP}/settings`)
    const select = page.getByRole('combobox', { name: /Static channel/ }).last()
    await expect(select).toHaveValue('149')
    await expect(select.locator('option[value="52"]')).toBeDisabled()
    await expect(select.locator('option[value="165"]')).toHaveCount(0)
  })

  test('switching on a protocol feature asks first and saves', async ({ signedIn: page }) => {
    await open(page, `/next/sites/home/aps/${AP}/settings`)
    await expandSection(page, 'Diagnostics')
    await page.getByRole('switch', { name: /Reply to ep-subscribe/ }).click()
    const bars = page.getByRole('region', { name: 'Save changes' })
    await bars.getByRole('button', { name: 'Save' }).click()
    const dialog = page.getByRole('alertdialog', { name: /Switch on 1 protocol feature/ })
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Switch on' }).click()
    await expect(page.getByText('Hallway saved')).toBeVisible()
  })

  test('the hostname preview follows the name', async ({ signedIn: page }) => {
    await open(page, `/next/sites/home/aps/${AP}/settings`)
    await page.getByLabel('Name', { exact: true }).fill('Hall Way')
    await expect(page.getByText('Hall Way', { exact: true }).last()).toBeVisible()
  })
})

test.describe('changes', () => {
  test('restore asks for a differing secret, previews, then restores', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/changes/45')
    await page.getByRole('button', { name: 'Restore this revision' }).click()
    await expect(page.getByText('Passphrase for Example-IoT at rev 45 differs from today’s')).toBeVisible()
    await page.getByRole('checkbox', { name: 'Keep the current value' }).check()
    await page.getByRole('button', { name: 'Check again' }).click()
    await expect(page.getByText('Changed back:')).toBeVisible()
    await page.getByRole('button', { name: 'Restore revision 45' }).click()
    await expect(page).toHaveURL(/\/next\/sites\/home\/changes$/)
    await expect(page.getByText('Restored revision 45', { exact: true }).first()).toBeVisible()
  })

  test('a saved change appears in the history', async ({ signedIn: page }) => {
    await open(page, '/next/sites/home/wlans/wlan-iot')
    await page.getByRole('switch', { name: 'Hidden SSID', exact: true }).click()
    await saveBar(page).getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('WLAN Example-IoT saved')).toBeVisible()
    // Client-side navigation keeps the mock state (a reload would reset it).
    if (isPhone(page)) {
      await page.getByRole('navigation', { name: 'Tabs' }).getByRole('button', { name: 'More' }).click()
      await page.getByRole('navigation', { name: 'More' }).getByRole('link', { name: 'Changes' }).click()
    } else await page.getByRole('navigation', { name: 'Primary' }).getByRole('link', { name: 'Changes' }).click()
    await expect(page.getByText('WLAN Example-IoT: hidden')).toBeVisible()
  })
})
