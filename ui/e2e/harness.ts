import { AxeBuilder } from '@axe-core/playwright'
import { expect, test as base, type Page } from '@playwright/test'

// Shared fixtures and checks for every UI spec (design §7.7, §7.8, §7.10).
// Feature workstreams import { test, expect, checkPage } from './harness'
// and add their routes to e2e/routes.ts.

declare global {
  interface Window {
    /** Mock controls installed by src/api/mocks/browser.ts in `--mode mock`. */
    __dismistMock?: { signIn(): void; signOut(): void; dropStreams(): void }
  }
}

export const MOCK_ADMIN_TOKEN = 'a1'.repeat(32)

type Fixtures = {
  /** Starts the test signed in (the MSW session flag) at the /next/ mount. */
  signedIn: Page
}

export const test = base.extend<Fixtures>({
  page: async ({ page }, use) => {
    // Silence the dev-only service worker log and keep theme deterministic.
    await page.addInitScript(() => {
      try {
        localStorage.setItem('dismist.theme', localStorage.getItem('dismist.theme') ?? 'light')
      } catch {
        /* storage unavailable */
      }
    })
    await use(page)
  },
  signedIn: async ({ page }, use) => {
    await page.addInitScript(() => localStorage.setItem('dismist.mock.session', '1'))
    await use(page)
  },
})

export { expect }

export function isPhone(page: Page) {
  return (page.viewportSize()?.width ?? 0) < 768
}

/** Rule 4: the page never scrolls sideways. */
export async function expectNoHorizontalScroll(page: Page) {
  const { scrollWidth, innerWidth } = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    innerWidth: window.innerWidth,
  }))
  expect(scrollWidth, 'document must not be wider than the viewport').toBeLessThanOrEqual(innerWidth)
}

/** Rule 1: text controls compute to 16 px below md and on every coarse pointer. */
export async function expectInputs16px(page: Page) {
  const result = await page.evaluate(() => {
    const coarse = matchMedia('(pointer: coarse)').matches
    const narrow = innerWidth < 768
    const controls = [...document.querySelectorAll<HTMLElement>('input, select, textarea, .cm-content')].filter((el) => {
      if (el instanceof HTMLInputElement && ['checkbox', 'radio', 'range', 'file', 'hidden'].includes(el.type)) return false
      if (el.closest('[aria-hidden="true"]') || el.classList.contains('sr-only')) return false
      const style = getComputedStyle(el)
      return style.display !== 'none' && style.visibility !== 'hidden'
    })
    return {
      enforced: coarse || narrow,
      count: controls.length,
      small: controls
        .map((el) => ({ id: el.id || el.getAttribute('name') || el.tagName.toLowerCase(), size: parseFloat(getComputedStyle(el).fontSize) }))
        .filter((c) => c.size < 16),
    }
  })
  if (result.enforced) expect(result.small, 'form controls below 16 px').toEqual([])
  return result
}

/** Rule 3: interactive elements are at least 44×44 below md. */
export async function expectTapTargets(page: Page, selector = 'main a, main button, header a, header button, nav a, nav button') {
  if (!isPhone(page)) return
  const small = await page.evaluate((sel) => {
    return [...document.querySelectorAll<HTMLElement>(sel)]
      .filter((el) => {
        const r = el.getBoundingClientRect()
        if (r.width === 0 || r.height === 0) return false
        if (el.closest('p, li > span, [data-inline-link]') && el.tagName === 'A' && getComputedStyle(el).display === 'inline') return false
        return r.width < 44 || r.height < 44
      })
      .map((el) => `${el.tagName.toLowerCase()} "${(el.getAttribute('aria-label') ?? el.textContent ?? '').trim().slice(0, 40)}" ${Math.round(el.getBoundingClientRect().width)}x${Math.round(el.getBoundingClientRect().height)}`)
  }, selector)
  expect(small, 'tap targets below 44 px').toEqual([])
}

/** axe (WCAG 2.2 AA) on the current page. */
export async function expectAccessible(page: Page) {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa']).analyze()
  const violations = results.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)
  expect(violations, 'axe violations').toEqual([])
}

/** The checks every route must pass at every width. */
export async function checkPage(page: Page) {
  await expectNoHorizontalScroll(page)
  await expectInputs16px(page)
  await expectTapTargets(page)
  await expectAccessible(page)
}

/** Waits until the app has rendered (the mock worker starts first). */
export async function ready(page: Page) {
  await page.locator('#root > *').first().waitFor()
  await page.waitForLoadState('networkidle')
}
