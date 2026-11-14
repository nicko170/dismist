import { describe, expect, it, vi } from 'vitest'
import { ApiError, applyFieldErrors, describeError, toApiError } from './errors'

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } })

describe('toApiError', () => {
  it('reads the {error, code, fields} contract', async () => {
    const error = await toApiError(
      json(400, { error: 'Check the highlighted fields.', code: 'invalid', fields: { secret: 'Must be 32 bytes.' } }),
    )
    expect(error).toBeInstanceOf(ApiError)
    expect(error.status).toBe(400)
    expect(error.code).toBe('invalid')
    expect(error.message).toBe('Check the highlighted fields.')
    expect(error.fields).toEqual({ secret: 'Must be 32 bytes.' })
  })

  it('falls back to a code from the status and a field message', async () => {
    const error = await toApiError(json(404, { fields: { id: 'No such AP.' } }))
    expect(error.code).toBe('not_found')
    expect(error.message).toBe('No such AP.')
  })

  it('turns pre-v1 plain text into one sentence', async () => {
    const error = await toApiError(new Response('invalid AP management IP\n', { status: 400 }))
    expect(error.code).toBe('invalid')
    expect(error.message).toBe('Invalid AP management IP')
  })

  it('never shows HTML or empty bodies', async () => {
    expect((await toApiError(new Response('<html>oops</html>', { status: 502 }))).message).toMatch(/internal error/)
    expect((await toApiError(new Response('', { status: 401 }))).message).toMatch(/session has ended/)
  })

  it('maps a non-JSON 403 to forbidden, never to an expert-patch code', async () => {
    const error = await toApiError(new Response('<html><h1>403 Forbidden</h1></html>', { status: 403 }))
    expect(error.code).toBe('forbidden')
    expect(error.message).toBe('The request was refused.')
  })

  it('has no fallback code for statuses the server never sends', async () => {
    expect((await toApiError(new Response('<html>Precondition Failed</html>', { status: 412 }))).code).toBe('invalid')
  })

  it('reads Retry-After on 429', async () => {
    const error = await toApiError(json(429, { error: 'Slow down.', code: 'rate_limited' }, { 'Retry-After': '30' }))
    expect(error.retryAfter).toBe(30)
    expect(error.code).toBe('rate_limited')
  })
})

describe('applyFieldErrors', () => {
  const error = new ApiError({
    status: 400,
    code: 'invalid',
    message: 'Check the highlighted fields.',
    fields: { secret: 'Must be 32 bytes.', 'radius.auth[0].host': 'Unreachable.' },
  })

  it('maps known fields onto setError and returns the rest', () => {
    const setError = vi.fn()
    const rest = applyFieldErrors(error, setError, ['secret', 'name'])
    expect(setError).toHaveBeenCalledWith('secret', { type: 'server', message: 'Must be 32 bytes.' }, { shouldFocus: true })
    expect(rest).toEqual(['Unreachable.'])
  })

  it('translates JSON paths through the map', () => {
    const setError = vi.fn()
    applyFieldErrors(error, setError, ['radiusHost', 'secret'], { 'radius.auth[0].host': 'radiusHost' })
    expect(setError).toHaveBeenCalledTimes(2)
  })

  it('returns the summary when nothing maps', () => {
    expect(applyFieldErrors(error, vi.fn(), ['name'])).toEqual(['Check the highlighted fields.', 'Must be 32 bytes.', 'Unreachable.'])
    expect(applyFieldErrors(new Error('boom'), vi.fn(), [])).toEqual(['boom'])
  })
})

describe('describeError', () => {
  it('exposes message and code for ErrorState', () => {
    expect(describeError(new ApiError({ status: 0, code: 'network', message: 'Offline.' }))).toEqual({ message: 'Offline.', code: 'network' })
    expect(describeError('nope')).toEqual({ message: 'Something went wrong.', code: null })
  })
})
