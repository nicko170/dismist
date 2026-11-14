import { beforeEach, describe, expect, it } from 'vitest'
import { getResponse } from 'msw'
import type { APDetail, EventPage } from '@/gen/api.gen'
import { normaliseDetail } from '@/api/queries/aps'
import { resetScreenMocks, screenHandlers } from './handlers'

// Contract checks for E's MSW handlers: typed with the generated shapes, and
// readable by the screens' normalisers.

const call = async (method: string, path: string, body?: unknown) => {
  const response = await getResponse(
    screenHandlers,
    new Request(`http://127.0.0.1${path}`, { method, body: body === undefined ? undefined : JSON.stringify(body), headers: { 'Content-Type': 'application/json' } }),
    { baseUrl: 'http://127.0.0.1' },
  )
  if (!response) throw new Error(`unhandled ${method} ${path}`)
  return { status: response.status, json: response.status === 204 ? null : await response.json().catch(() => null) }
}

describe('screen mocks', () => {
  beforeEach(() => resetScreenMocks())

  it('serves a full APDetail with the scan radio and bug-B reboot time', async () => {
    const { json } = await call('GET', '/api/aps/02-aa-00-00-00-01')
    const detail = normaliseDetail(json) as APDetail
    expect(detail.radios_detail.map((r) => r.role)).toEqual(['access', 'access', 'scan'])
    expect(detail.identity.last_reboot_time).toBe(detail.connected_at)
  })

  it('pages events with before= and filters by AP', async () => {
    const first = (await call('GET', '/api/sites/home/events?limit=50')).json as EventPage
    expect(first.events).toHaveLength(50)
    expect(first.next_before).toBeTruthy()
    const second = (await call('GET', `/api/sites/home/events?limit=50&before=${encodeURIComponent(first.next_before!)}`)).json as EventPage
    expect(second.events.length).toBeGreaterThan(0)
    expect(second.events.every((e) => e.last_seen < first.next_before!)).toBe(true)
    const garage = (await call('GET', '/api/sites/home/events?ap=02-aa-00-00-00-02')).json as EventPage
    expect(garage.events.every((e) => e.ap_id === '02-aa-00-00-00-02')).toBe(true)
  })

  it('refuses ap-cmd actions without the capability and accepts locate', async () => {
    expect((await call('POST', '/api/aps/02-aa-00-00-00-01/actions/reboot', {})).json).toMatchObject({ code: 'unverified_command' })
    expect((await call('POST', '/api/aps/02-aa-00-00-00-01/actions/locate', { minutes: 5 })).json).toMatchObject({ state: 'accepted' })
  })

  it('dismisses a pending AP', async () => {
    expect((await call('DELETE', '/api/aps/pending/02-aa-00-00-00-20?days=30')).status).toBe(204)
    expect((await call('GET', '/api/aps/pending')).json).toEqual([])
    expect((await call('GET', '/api/aps/pending?dismissed=1')).json).toHaveLength(1)
  })
})
