import { isPhone, ready } from '../harness'
import { expect, test } from './fixture'

// Behaviour of workstream E's screens against the mocks (design §3.2-§3.5,
// §3.12-§3.14).

const AP = '02-aa-00-00-00-01'

test.describe('AP list', () => {
  test('filters by status in the URL and links rows to the detail', async ({ signedIn: page }) => {
    await page.goto('/next/sites/home/aps')
    await ready(page)
    await page.getByRole('navigation', { name: 'Filter by status' }).getByRole('link', { name: /Offline/ }).click()
    await expect(page).toHaveURL(/status=offline/)
    await expect(page.getByRole('link', { name: 'Garage' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Hallway' })).toHaveCount(0)
    await page.getByRole('navigation', { name: 'Filter by status' }).getByRole('link', { name: /^All/ }).click()
    await page.getByRole('link', { name: 'Hallway' }).click()
    await expect(page).toHaveURL(new RegExp(`/aps/${AP}$`))
    await expect(page.getByRole('heading', { level: 1, name: 'Hallway' })).toBeVisible()
  })

  test('pending APs can be registered or dismissed', async ({ signedIn: page }) => {
    await page.goto('/next/sites/home/aps')
    await ready(page)
    await expect(page.getByText('1 AP is trying to connect.')).toBeVisible()
    await expect(page.getByRole('link', { name: 'Register 02-aa-00-00-00-20' })).toHaveAttribute('href', /\/onboard\/register\?id=02-aa-00-00-00-20&site=home/)
    await page.getByRole('button', { name: 'Dismiss 02-aa-00-00-00-20 for 30 days' }).click()
    await expect(page.getByText('1 AP is trying to connect.')).toBeHidden()
  })

  test('live client counts arrive over the site stream', async ({ signedIn: page }) => {
    test.slow()
    await page.goto('/next/sites/home/aps')
    await ready(page)
    const before = await page.evaluate(() => document.body.innerText)
    await expect.poll(async () => (await page.evaluate(() => document.body.innerText)) !== before, { timeout: 15_000 }).toBe(true)
  })
})

test.describe('AP detail', () => {
  test('header shows the identity and connection facts from the AP', async ({ signedIn: page }) => {
    await page.goto(`/next/sites/home/aps/${AP}`)
    await ready(page)
    const facts = page.locator('dl').first()
    await expect(facts.getByText('LAN IP')).toBeVisible()
    await expect(facts.getByText('192.0.2.21', { exact: true })).toBeVisible()
    await expect(facts.getByText('via NAT')).toBeVisible()
    await expect(facts.getByText('AP33-WW', { exact: true }).first()).toBeVisible()
    await expect(facts.getByText('tested', { exact: true })).toBeVisible()
    // Bug B: the reported reboot time equals the hello, so it comes from uptime.
    await expect(facts.getByText('(from uptime)')).toBeVisible()
    await expect(facts.getByText('Any (identified by ID)')).toBeVisible()
  })

  test('untested firmware is flagged', async ({ signedIn: page }) => {
    await page.goto('/next/sites/home/aps/02-aa-00-00-00-03')
    await ready(page)
    await expect(page.getByText('untested firmware')).toBeVisible()
  })

  test('radios tab labels the scanning radio and shows neighbours', async ({ signedIn: page }) => {
    await page.goto(`/next/sites/home/aps/${AP}/radios`)
    await ready(page)
    await expect(page.getByRole('heading', { name: 'Scanning radio (r2)' })).toBeVisible()
    await expect(page.getByText('Scanning radio, used for neighbour and utilisation data')).toBeVisible()
    await expect(page.getByRole('heading', { name: '5 GHz (r0)' })).toBeVisible()
    await expect(page.getByText('max 17 dBm').first()).toBeVisible()
    await expect(page.getByText('Neighbour-5G').filter({ visible: true })).toBeVisible()
  })

  test('uplink tab shows LLDP, PoE and an inactive VLAN', async ({ signedIn: page }) => {
    await page.goto(`/next/sites/home/aps/${AP}/uplink`)
    await ready(page)
    await expect(page.getByText('switch-example')).toBeVisible()
    await expect(page.getByText('VLAN 30 is not reaching this AP’s uplink.')).toBeVisible()
    await expect(page.getByText('19.2 W')).toBeVisible()
  })

  test('reboot without a verified ap-cmd gives the LAN-side command', async ({ signedIn: page }) => {
    await page.goto(`/next/sites/home/aps/${AP}`)
    await ready(page)
    if (isPhone(page)) {
      await page.getByRole('button', { name: 'Actions' }).click()
      await page.getByRole('button', { name: /^Reboot/ }).click()
    } else {
      await page.getByRole('button', { name: 'More actions' }).click()
      await page.getByRole('menuitem', { name: /Reboot/ }).click()
    }
    const dialog = page.getByRole('dialog', { name: 'Reboot Hallway' })
    await expect(dialog.getByText('dismist ap33 ssh root@192.0.2.21 reboot')).toBeVisible()
    await dialog.getByRole('button', { name: 'Done' }).click()
    await expect(dialog).toBeHidden()
  })

  test('locate is experimental and needs the site switch', async ({ signedIn: page }) => {
    await page.goto(`/next/sites/home/aps/${AP}`)
    await ready(page)
    if (isPhone(page)) {
      await page.getByRole('button', { name: 'Actions' }).click()
      await page.getByRole('button', { name: /^Locate/ }).click()
    } else {
      await page.getByRole('button', { name: 'Locate' }).click()
    }
    const dialog = page.getByRole('dialog', { name: 'Locate Hallway' })
    await expect(dialog.getByText('Experimental options are off for this site.')).toBeVisible()
    await expect(dialog.getByRole('button', { name: /Locate for 5 min/ })).toBeDisabled()
  })

  test('delete needs the AP name typed', async ({ signedIn: page }) => {
    test.skip(isPhone(page), 'the same dialog; exercised at desktop widths')
    await page.goto(`/next/sites/home/aps/${AP}`)
    await ready(page)
    await page.getByRole('button', { name: 'More actions' }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    const dialog = page.getByRole('alertdialog', { name: 'Delete Hallway?' })
    await expect(dialog.getByRole('button', { name: 'Delete AP' })).toBeDisabled()
    await dialog.getByLabel(/Type/).fill('Hallway')
    await dialog.getByRole('button', { name: 'Delete AP' }).click()
    await expect(page).toHaveURL(/\/sites\/home\/aps$/)
  })
})

test.describe('events', () => {
  test('filters, pages older events and opens the typed detail', async ({ signedIn: page }) => {
    await page.goto('/next/sites/home/events')
    await ready(page)
    const list = page.getByRole('list', { name: 'Events' })
    await expect(list.getByRole('listitem')).toHaveCount(50)
    await page.getByRole('button', { name: 'Load older events' }).click()
    await expect(list.getByRole('listitem')).toHaveCount(74)
    await list.getByRole('button', { name: /DHCP failure on VLAN 30/ }).click()
    const drawer = page.getByRole('dialog', { name: 'DHCP failure on VLAN 30' })
    await expect(drawer.getByText('Attempts', { exact: true })).toBeVisible()
    await expect(drawer.getByText('4', { exact: true })).toBeVisible()
    await drawer.getByRole('button', { name: 'Close' }).click()
    await page.goto('/next/sites/home/events?kind=authorized')
    await ready(page)
    await list.getByRole('button', { name: /phone connected to Example/ }).click()
    await expect(page.getByRole('dialog').getByText('4-way handshake')).toBeVisible()
  })
})

test.describe('clients', () => {
  test('search narrows the list and the detail labels traffic direction', async ({ signedIn: page }) => {
    await page.goto('/next/sites/home/clients?q=print')
    await ready(page)
    await expect(page.getByRole('link', { name: 'printer' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'phone' })).toHaveCount(0)
    await page.goto('/next/sites/home/clients')
    await ready(page)
    await expect(page.getByText(/DHCP failing/).filter({ visible: true }).first()).toBeVisible()
    await page.getByRole('link', { name: 'phone' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'phone' })).toBeVisible()
    await expect(page.getByText('Download (to client)')).toBeVisible()
    await expect(page.getByText('Upload (from client)')).toBeVisible()
  })
})

test.describe('sites', () => {
  test('New site creates and replaces the dialog entry', async ({ signedIn: page }) => {
    await page.goto('/next/sites')
    await ready(page)
    await page.getByRole('button', { name: 'New site' }).click()
    await expect(page).toHaveURL(/dialog=new-site/)
    const dialog = page.getByRole('dialog', { name: 'New site' })
    await dialog.getByRole('button', { name: 'Create site' }).click()
    await expect(dialog.getByText('Enter a name.')).toBeVisible()
    await dialog.getByLabel('Name').fill('Beach house')
    await dialog.getByLabel('Country').selectOption('AU')
    await dialog.getByRole('button', { name: 'Create site' }).click()
    await expect(page).toHaveURL(/\/next\/sites\/beach-house\/overview$/)
    await page.goBack()
    await expect(page).toHaveURL(/\/next\/sites$/)
  })
})

test.describe('system', () => {
  test('shows certificate expiry and creates a token shown once', async ({ signedIn: page }) => {
    await page.goto('/next/system')
    await ready(page)
    await expect(page.getByText(/d left/).first()).toBeVisible()
    await page.getByRole('navigation', { name: 'System sections' }).getByRole('link', { name: 'API tokens' }).click()
    await page.getByRole('button', { name: 'Create token' }).first().click()
    const dialog = page.getByRole('dialog', { name: 'Create an API token' })
    await dialog.getByLabel('Name').fill('CI')
    await dialog.getByRole('button', { name: 'Create token' }).click()
    await expect(page.getByRole('dialog', { name: 'Token created' }).getByText(/_synthetic-secret-for-tests$/)).toBeVisible()
  })

  test('migration report shows the lifted live patches', async ({ signedIn: page }) => {
    await page.goto('/next/system/migration')
    await ready(page)
    await expect(page.getByText('Committed').first()).toBeVisible()
    await expect(page.getByText('{"Monitor":{"CCRev":""}}')).toBeVisible()
    await expect(page.getByText('wpa2_wpa3_personal')).toBeVisible()
  })
})

test.describe('global search', () => {
  test('finds a client by name and opens it', async ({ signedIn: page }) => {
    await page.goto('/next/sites/home/overview')
    await ready(page)
    await page.getByRole('button', { name: /^Search/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Search' })
    await dialog.getByRole('combobox').fill('printer')
    await dialog.getByRole('option', { name: /printer/ }).click()
    await expect(page).toHaveURL(/\/sites\/home\/clients\/00-aa-bb-00-00-03$/)
    await expect(page.getByRole('heading', { level: 1, name: 'printer' })).toBeVisible()
  })
})
