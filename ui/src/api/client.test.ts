import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, buildUrl, onUnauthorized, request } from './client'
import { ApiError } from './errors'

afterEach(() => onUnauthorized(null))

function mockFetch(response: Response | Error) {
  const fetch = vi.fn(async () => {
    if (response instanceof Error) throw response
    return response
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

describe('request', () => {
  it('sends JSON, If-Match and query parameters', async () => {
    const fetch = mockFetch(new Response('{"ok":true}', { status: 200 }))
    await expect(api.patch('/api/aps/x', { name: 'Hallway' }, { ifMatch: 7, query: { dry_run: 1, empty: '' } })).resolves.toEqual({ ok: true })
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/aps/x?dry_run=1')
    expect(init.method).toBe('PATCH')
    expect(init.body).toBe('{"name":"Hallway"}')
    expect((init.headers as Record<string, string>)['If-Match']).toBe('"7"')
    expect(init.credentials).toBe('same-origin')
  })

  it('calls the 401 handler and throws ApiError', async () => {
    mockFetch(new Response('{"error":"Sign in to continue.","code":"unauthorized"}', { status: 401 }))
    const handler = vi.fn()
    onUnauthorized(handler)
    await expect(request('GET', '/api/sites')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(handler).toHaveBeenCalledOnce()
  })

  it('skips the redirect for session probes', async () => {
    mockFetch(new Response('{"authenticated":false}', { status: 401 }))
    const handler = vi.fn()
    onUnauthorized(handler)
    await expect(request('GET', '/auth/session', { skipAuthRedirect: true })).rejects.toBeInstanceOf(ApiError)
    expect(handler).not.toHaveBeenCalled()
  })

  it('maps network failures to code "network"', async () => {
    mockFetch(new TypeError('Failed to fetch'))
    await expect(api.get('/api/system')).rejects.toMatchObject({ status: 0, code: 'network' })
  })

  it('returns undefined for 204', async () => {
    mockFetch(new Response(null, { status: 204 }))
    await expect(api.delete('/api/aps/x')).resolves.toBeUndefined()
  })
})

describe('buildUrl', () => {
  it('repeats arrays and drops empty values', () => {
    expect(buildUrl('/api/x', { band: ['2.4', '5'], q: undefined, n: 0 })).toBe('/api/x?band=2.4&band=5&n=0')
  })
})
