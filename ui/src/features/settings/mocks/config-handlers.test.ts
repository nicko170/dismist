import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { setupServer } from 'msw/node'
import { ApiError } from '@/api/errors'
import { patchNetwork, networkQuery } from '@/api/queries/networks'
import { restoreChange } from '@/api/queries/changes'
import { putRF, rfQuery } from '@/api/queries/rf'
import { patchSite } from '@/api/queries/sites'
import { configHandlers, resetConfigMocks } from './config-handlers'
import { toRFForm, toRFInput } from '@/features/rf/form'

// The mocks are the contract examples F's screens run against until C lands
// the phase-2 endpoints: dry runs return the impact with a redacted diff and
// store nothing, If-Match mismatches answer 409 stale with the current object,
// experimental values are refused on a non-experimental site, and restore asks
// for a differing secret.

const server = setupServer(...configHandlers)

beforeAll(() => server.listen({ onUnhandledFrame: 'error' }))
afterAll(() => server.close())
beforeEach(() => {
  resetConfigMocks()
  localStorage.setItem('dismist.mock.session', '1')
})

async function getJSON<T>(query: { queryFn?: unknown }): Promise<T> {
  return (query.queryFn as (ctx: { signal: AbortSignal }) => Promise<T>)({ signal: new AbortController().signal })
}

describe('config mocks', () => {
  it('dry-runs a WLAN edit without storing it', async () => {
    const dry = await patchNetwork('home', 'wlan-example', { hidden: true }, { dryRun: true, ifMatch: 5 })
    expect(dry.dry_run).toBe(true)
    expect(dry.impact.aps).toHaveLength(3)
    expect(dry.impact.aps[0].diff).toContainEqual({ path: 'WLANs[Example].hidden', from: false, to: true })
    expect(dry.impact.summary).toMatch(/WLAN Example restarts on 3 APs/)
    const stored = await getJSON<{ hidden: boolean }>(networkQuery('home', 'wlan-example'))
    expect(stored.hidden).toBe(false)
  })

  it('answers 409 stale with the current object', async () => {
    await patchNetwork('home', 'wlan-example', { dtim: 3 }, { ifMatch: 5 })
    const error = await patchNetwork('home', 'wlan-example', { hidden: true }, { ifMatch: 5 }).catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).code).toBe('stale')
    expect(((error as ApiError).body as { current: { updated_rev: number } }).current.updated_rev).toBe(6)
  })

  it('refuses experimental values on a non-experimental site', async () => {
    const error = (await patchNetwork('home', 'wlan-example', { security: 'owe' }).catch((e: unknown) => e)) as ApiError
    expect(error.code).toBe('experimental_disabled')
    expect(error.fields.security).toMatch(/experimental/)
    await expect(patchNetwork('lab', 'wlan-lab', { security: 'owe' })).resolves.toMatchObject({ object: { security: 'owe' } })
  })

  it('pushes nothing for a dismist-only site edit', async () => {
    const response = await patchSite('home', { name: 'Home 2' }, { dryRun: true })
    expect(response.impact.aps).toEqual([])
    expect(response.impact.summary).toBe('No AP changes')
  })

  it('previews a radio change as a radio restart and never sends the Auto list', async () => {
    const view = await getJSON<Parameters<typeof toRFForm>[0]>(rfQuery('home'))
    const form = toRFForm(view)
    const channelsOnly = await putRF('home', toRFInput({ ...form, band_5: { ...form.band_5, channels: [36] } }, view), { dryRun: true })
    expect(channelsOnly.impact.aps).toEqual([])
    const width = await putRF('home', toRFInput({ ...form, band_5: { ...form.band_5, width: 40 } }, view), { dryRun: true })
    expect(width.impact.disruption).toBe('radio_bounce')
  })

  it('asks for a differing secret on restore', async () => {
    const error = (await restoreChange('home', 45, {}, { dryRun: true }).catch((e: unknown) => e)) as ApiError
    expect(error.code).toBe('secret_required')
    expect(Object.keys(error.fields)).toEqual(['networks.wlan-iot.psk'])
    const preview = await restoreChange('home', 45, { secrets: { 'networks.wlan-iot.psk': { keep: true } } }, { dryRun: true })
    expect(preview.object.changed).toEqual([{ kind: 'network', id: 'wlan-iot', name: 'Example-IoT' }])
  })
})
