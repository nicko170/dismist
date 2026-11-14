import { getResponse } from 'msw'
import { test as harness } from '../harness'
import { resetScreenMocks, screenHandlers } from '../../src/features/overview/mocks/handlers'

// Workstream E's Playwright fixture. WS-D's mock build serves the shell's
// endpoints and the site stream through its service worker; the extra
// endpoints E's screens read (src/features/overview/mocks/handlers.ts) are
// served here: an init script wraps window.fetch, and matching /api/
// requests are answered by those MSW handlers running in the test process
// (msw getResponse). Anything they do not handle falls through to the
// service worker. Once WS-D registers screenHandlers in the browser worker
// this shim becomes a no-op duplicate and can be dropped.

type Wire = { status: number; headers: Record<string, string>; body: string | null }

export const test = harness.extend({
  page: async ({ page }, use) => {
    resetScreenMocks()
    await page.exposeFunction('dismistScreenMock', async (method: string, url: string, headers: Record<string, string>, body: string | null): Promise<Wire | null> => {
      const request = new Request(url, { method, headers, body: body ?? undefined })
      const response = await getResponse(screenHandlers, request, { baseUrl: new URL(url).origin })
      if (!response) return null
      const text = response.status === 204 || response.status === 304 ? null : await response.text()
      return { status: response.status, headers: Object.fromEntries(response.headers), body: text }
    })
    await page.addInitScript(() => {
      const original = window.fetch.bind(window)
      const bridge = (window as unknown as { dismistScreenMock: (m: string, u: string, h: Record<string, string>, b: string | null) => Promise<Wire | null> }).dismistScreenMock
      window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
        const request = new Request(input, init)
        const url = new URL(request.url, location.href)
        if (url.origin !== location.origin || !url.pathname.startsWith('/api/')) return original(input, init)
        const body = request.method === 'GET' || request.method === 'HEAD' ? null : await request.clone().text()
        const wire = await bridge(request.method, url.href, Object.fromEntries(request.headers), body)
        if (!wire) return original(input, init)
        return new Response(wire.body, { status: wire.status, headers: wire.headers })
      }
    })
    await use(page)
  },
})

export { expect } from '../harness'
