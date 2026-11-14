import { beforeEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, type InfiniteData } from '@tanstack/react-query'
import { keys } from './keys'
import { BACKOFF_MS, OFFLINE_AFTER_MS, SiteStream, applyStreamBatch, getStreamStatus, invalidateSite } from './sse'
import type { APLive, APView, EventPage } from './types'
import { aps } from './mocks/data'
import { toLive } from './mocks/handlers'

class FakeEventSource extends EventTarget {
  static instances: FakeEventSource[] = []
  readyState = 0
  closed = false
  constructor(readonly url: string) {
    super()
    FakeEventSource.instances.push(this)
  }
  open() {
    this.readyState = 1
    this.dispatchEvent(new Event('open'))
  }
  emit(type: string, data: unknown) {
    this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }))
  }
  fail(closed: boolean) {
    this.readyState = closed ? 2 : 0
    this.dispatchEvent(new Event('error'))
  }
  close() {
    this.readyState = 2
    this.closed = true
  }
}

const site = 'home'
const homeAPs = () => aps.filter((ap) => ap.site_id === site).map((ap) => ({ ...ap }))

function setup(probe: () => Promise<'authenticated' | 'expired' | 'unknown'> = async () => 'authenticated') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onExpired = vi.fn()
  const doc = Object.assign(new EventTarget(), { visibilityState: 'visible' as DocumentVisibilityState })
  const stream = new SiteStream({
    siteId: site,
    queryClient: qc,
    probeSession: probe,
    onExpired,
    env: {
      createEventSource: (url) => new FakeEventSource(url) as unknown as EventSource,
      frame: (fn) => fn(),
      document: doc as unknown as Document,
    },
  })
  return { qc, stream, onExpired, doc, source: () => FakeEventSource.instances.at(-1)! }
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.useFakeTimers()
})

describe('applyStreamBatch', () => {
  it('merges ap updates into the list and detail without refetching', () => {
    const qc = new QueryClient()
    qc.setQueryData(keys.siteAPs(site), homeAPs())
    qc.setQueryData(keys.ap('02-aa-00-00-00-01'), homeAPs()[0])
    const live: APLive = { ...toLive(homeAPs()[0]), client_count: 99, lan_ip: '192.0.2.99' }
    applyStreamBatch(qc, site, [{ type: 'ap', data: live }])
    const list = qc.getQueryData<APView[]>(keys.siteAPs(site))!
    expect(list[0].client_count).toBe(99)
    expect(list[0].behind_nat).toBe(true)
    expect(qc.getQueryData<APView>(keys.ap('02-aa-00-00-00-01'))!.lan_ip).toBe('192.0.2.99')
  })

  it('snapshot drops APs that left the site and refetches for unknown ones', () => {
    const qc = new QueryClient()
    qc.setQueryData(keys.siteAPs(site), homeAPs())
    const spy = vi.spyOn(qc, 'invalidateQueries')
    const [first] = homeAPs()
    applyStreamBatch(qc, site, [
      { type: 'snapshot', data: { site_id: site, revision: 9, aps: [toLive(first), { ...toLive(first), id: '02-aa-00-00-00-99' }] } },
    ])
    expect(qc.getQueryData<APView[]>(keys.siteAPs(site))!.map((ap) => ap.id)).toEqual([first.id])
    expect(spy).toHaveBeenCalledWith({ queryKey: keys.siteAPs(site), exact: true })
  })

  it('upserts events into the cached first page, else counts them', () => {
    const qc = new QueryClient()
    const summary = { id: 'ev-9', ap_id: '02-aa-00-00-00-01', kind: 'radar', severity: 'warning' as const, title: 'Radar on 52', first_seen: 'x', last_seen: 'x', count: 1, resolved_by: null, has_client: false }
    applyStreamBatch(qc, site, [{ type: 'event', data: summary }])
    expect(qc.getQueryData(keys.siteNewEvents(site))).toBe(1)

    qc.setQueryData<InfiniteData<EventPage>>(keys.siteEvents(site, {}), { pages: [{ events: [], next_before: null }], pageParams: [null] })
    applyStreamBatch(qc, site, [{ type: 'event', data: summary }, { type: 'event', data: { ...summary, count: 2 } }])
    const page = qc.getQueryData<InfiniteData<EventPage>>(keys.siteEvents(site, {}))!.pages[0]
    expect(page.events).toHaveLength(1)
    expect(page.events[0]).toMatchObject({ id: 'ev-9', count: 2, site_id: site, client_mac: '' })
  })

  it('config invalidates the object kind and affected AP configs', () => {
    const qc = new QueryClient()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    applyStreamBatch(qc, site, [{ type: 'config', data: { revision: 5, kind: 'wlan.updated', object_id: 'n1', affected_aps: ['a1'], actor: null } }])
    const calls = spy.mock.calls.map(([arg]) => (arg as { queryKey: unknown }).queryKey)
    expect(calls).toContainEqual(keys.siteNetworks(site))
    expect(calls).toContainEqual(keys.network('n1'))
    expect(calls).toContainEqual(keys.apConfig('a1'))
  })

  it('push and pending update their keys', () => {
    const qc = new QueryClient()
    applyStreamBatch(qc, site, [
      { type: 'push', data: { ap_id: 'a1', revision: 4, state: 'applied', error: '' } },
      { type: 'pending', data: { count: 2 } },
    ])
    expect(qc.getQueryData(keys.sitePush(site))).toMatchObject({ a1: { state: 'applied' } })
    expect(qc.getQueryData(keys.pendingCount)).toBe(2)
  })
})

describe('invalidateSite', () => {
  it('covers the site subtree and entities whose site_id matches', () => {
    const qc = new QueryClient()
    qc.setQueryData(keys.ap('a1'), { site_id: site })
    qc.setQueryData(keys.apTelemetry('a1'), { radios: [] })
    qc.setQueryData(keys.ap('a2'), { site_id: 'lab' })
    qc.setQueryData(keys.siteAPs(site), [])
    invalidateSite(qc, site)
    const stale = (key: readonly unknown[]) => qc.getQueryState(key)?.isInvalidated
    expect(stale(keys.siteAPs(site))).toBe(true)
    expect(stale(keys.ap('a1'))).toBe(true)
    expect(stale(keys.apTelemetry('a1'))).toBe(true)
    expect(stale(keys.ap('a2'))).toBe(false)
  })
})

describe('SiteStream', () => {
  it('batches a burst of updates into one flush', async () => {
    const { qc, stream, source } = setup()
    qc.setQueryData(keys.siteAPs(site), homeAPs())
    const spy = vi.spyOn(qc, 'setQueryData')
    stream.start()
    source().open()
    for (let i = 0; i < 20; i++) source().emit('ap', { ...toLive(homeAPs()[0]), client_count: i })
    expect(spy).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(0)
    expect(qc.getQueryData<APView[]>(keys.siteAPs(site))![0].client_count).toBe(19)
    const writes = spy.mock.calls.filter(([key]) => JSON.stringify(key) === JSON.stringify(keys.siteAPs(site))).length
    expect(writes).toBe(20)
    spy.mockClear()
    source().emit('ap', { ...toLive(homeAPs()[0]), client_count: 1 })
    await vi.advanceTimersByTimeAsync(100)
    expect(spy).not.toHaveBeenCalled() // at most one flush per 250 ms
    await vi.advanceTimersByTimeAsync(200)
    expect(qc.getQueryData<APView[]>(keys.siteAPs(site))![0].client_count).toBe(1)
    stream.stop()
  })

  it('invalidates the site on every reopen but not the first open', async () => {
    const { qc, stream, source } = setup()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    stream.start()
    source().open()
    expect(getStreamStatus()).toBe('live')
    expect(spy).not.toHaveBeenCalled()
    source().fail(false) // native retry
    await vi.advanceTimersByTimeAsync(0)
    expect(getStreamStatus()).toBe('reconnecting')
    source().open()
    expect(spy).toHaveBeenCalledWith({ queryKey: keys.site(site) })
    stream.stop()
  })

  it('redirects to login when the probe finds the session expired', async () => {
    const { stream, source, onExpired } = setup(async () => 'expired')
    stream.start()
    source().open()
    source().fail(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(onExpired).toHaveBeenCalledOnce()
    expect(source().closed).toBe(true)
    expect(getStreamStatus()).toBe('idle')
  })

  it('reopens a closed stream with backoff and goes offline after 30 s', async () => {
    const { stream, source } = setup()
    stream.start()
    source().open()
    source().fail(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(FakeEventSource.instances).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(BACKOFF_MS[0])
    expect(FakeEventSource.instances).toHaveLength(2)
    source().fail(true)
    await vi.advanceTimersByTimeAsync(BACKOFF_MS[1] - 1)
    expect(FakeEventSource.instances).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeEventSource.instances).toHaveLength(3)
    await vi.advanceTimersByTimeAsync(OFFLINE_AFTER_MS)
    expect(getStreamStatus()).toBe('offline')
    source().open()
    expect(getStreamStatus()).toBe('live')
    stream.stop()
  })

  it('closes after 30 s hidden and reopens (and invalidates) when visible', async () => {
    const { qc, stream, source, doc } = setup()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    stream.start()
    source().open()
    doc.visibilityState = 'hidden'
    doc.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(30_000)
    expect(source().closed).toBe(true)
    doc.visibilityState = 'visible'
    doc.dispatchEvent(new Event('visibilitychange'))
    expect(FakeEventSource.instances).toHaveLength(2)
    source().open()
    expect(spy).toHaveBeenCalledWith({ queryKey: keys.site(site) })
    stream.stop()
  })
})
