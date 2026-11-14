import { MOCK_ADMIN_TOKEN, expect, expectInputs16px, ready, test } from './harness'

test.describe('sign-in', () => {
  test('redirects to login with next, then back after sign-in', async ({ page }) => {
    await page.goto('/next/sites/home/aps?status=offline')
    await expect(page).toHaveURL(/\/next\/login\?next=/)
    const next = new URL(page.url()).searchParams.get('next')
    expect(next).toBe('/sites/home/aps?status=offline')
    await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible()
    const inputs = await expectInputs16px(page)
    expect(inputs.count).toBeGreaterThan(0)

    await page.getByLabel('Token', { exact: true }).fill(MOCK_ADMIN_TOKEN)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(/\/next\/sites\/home\/aps\?status=offline$/)
  })

  test('shows an inline error that clears on the next keystroke', async ({ page }) => {
    await page.goto('/next/login')
    await ready(page)
    const token = page.getByLabel('Token', { exact: true })
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('alert')).toHaveText('Enter your token.')
    await expect(token).toHaveAttribute('aria-invalid', 'true')

    await token.fill('b2'.repeat(32))
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('alert')).toHaveText('That token is not valid for this controller.')
    await expect(token).toBeFocused()
    await token.press('x')
    await expect(page.getByRole('alert')).toHaveCount(0)
    await expect(token).not.toHaveAttribute('aria-invalid', 'true')
  })

  test('show/hide reveals the token', async ({ page }) => {
    await page.goto('/next/login')
    await ready(page)
    const token = page.getByLabel('Token', { exact: true })
    await expect(token).toHaveAttribute('type', 'password')
    await page.getByRole('button', { name: 'Show token' }).click()
    await expect(token).toHaveAttribute('type', 'text')
  })

  test('rejects an off-site next', async ({ page }) => {
    await page.goto('/next/login?next=%2F%2Fexample.net%2F')
    await ready(page)
    await page.getByLabel('Token', { exact: true }).fill(MOCK_ADMIN_TOKEN)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page).toHaveURL(/127\.0\.0\.1:\d+\/next\/(sites|onboard)/)
  })

  test('the same bundle works at / (basepath chosen at runtime)', async ({ signedIn: page }) => {
    await page.goto('/sites/home')
    await expect(page).toHaveURL(/127\.0\.0\.1:\d+\/sites\/home\/overview$/)
    await page.goto('/next/sites/home')
    await expect(page).toHaveURL(/\/next\/sites\/home\/overview$/)
  })
})

test('touch devices get 16 px inputs at every width; mouse users get 14 px from md', async ({ page }, testInfo) => {
  await page.goto('/next/login')
  await ready(page)
  const { coarse, size } = await page.evaluate(() => ({
    coarse: matchMedia('(pointer: coarse)').matches,
    size: getComputedStyle(document.querySelector('input[type=password]')!).fontSize,
  }))
  const touch = Boolean(testInfo.project.use.hasTouch)
  expect(coarse).toBe(touch)
  const width = page.viewportSize()!.width
  expect(size).toBe(touch || width < 768 ? '16px' : '14px')
})
