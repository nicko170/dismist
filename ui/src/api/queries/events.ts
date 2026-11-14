import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale, type EventFilters } from '@/api/keys'
import type { EventPage, EventView } from '@/gen/api.gen'

// Stored events (design §3.13, §4.9, §5.8; workstream E): newest first,
// cursor paging with before=. The stream upserts summaries into the first
// page of the unfiltered query (keys.siteEvents(id, {})); a filtered list
// shows the "New events" pill instead.

export const EVENT_RANGES = { '1h': 3600_000, '24h': 86400_000, '7d': 7 * 86400_000, '30d': 30 * 86400_000 } as const
export type EventRange = keyof typeof EVENT_RANGES
export { EVENT_KINDS, EVENT_SEVERITIES } from '@/features/overview/kit/params'
const kindLabels: Record<string, string> = {
  connection: 'Connection',
  config: 'Config',
  reboot: 'Reboot',
  auth: 'Authentication',
  dhcp_failure: 'DHCP failure',
  dns: 'DNS',
  dns_server: 'DNS server',
  dns_latency: 'DNS latency',
  disassociated: 'Disassociated',
  authorized: 'Client connected',
  rsn_config: 'Radio config',
  radio: 'Radio',
  radar: 'Radar',
  port: 'Port',
  inactive_vlan: 'Inactive VLAN',
  clock: 'Clock',
  push: 'Config push',
  feature: 'Protocol feature',
  system: 'System',
  backup: 'Backup',
  gtk_rekey: 'Group key rekey',
  pace: 'AP event',
}

export function kindLabel(kind: string): string {
  return kindLabels[kind] ?? kind.replace(/_/g, ' ').replace(/^\w/, (c) => c.toUpperCase())
}

export const PAGE_SIZE = 50

export function normaliseEventPage(body: unknown): EventPage {
  const row = (body ?? {}) as { events?: unknown; next_before?: unknown }
  const events = (Array.isArray(body) ? body : Array.isArray(row.events) ? row.events : []).filter(
    (e): e is EventView => typeof e === 'object' && e !== null && typeof (e as EventView).id === 'string',
  )
  return {
    events: events.map((e) => ({ ...e, count: e.count || 1, data: e.data ?? {}, ap_name: e.ap_name ?? '' })),
    next_before: typeof row.next_before === 'string' && row.next_before ? row.next_before : null,
  }
}

/** The filter object used in the query key; empty values are dropped so `{}` is the default list. */
export function eventFilters(filters: EventFilters): EventFilters {
  const out: EventFilters = {}
  for (const [key, value] of Object.entries(filters) as [keyof EventFilters, string | undefined][]) if (value) out[key] = value
  return out
}

/**
 * Stored events, newest first. `before` (a shared deep link to older events)
 * starts the list at that cursor and gets its own cache entry; the default
 * list is keys.siteEvents(id, {}), which the stream patches.
 */
export const siteEventsQuery = (siteId: string, filters: EventFilters, { limit = PAGE_SIZE, before }: { limit?: number; before?: string } = {}) =>
  infiniteQueryOptions({
    queryKey: before
      ? ([...keys.siteEvents(siteId, eventFilters(filters)), { before }] as const)
      : keys.siteEvents(siteId, eventFilters(filters)),
    queryFn: async ({ pageParam, signal }) => {
      const since = filters.range && filters.range in EVENT_RANGES ? new Date(Date.now() - EVENT_RANGES[filters.range as EventRange]).toISOString() : undefined
      return normaliseEventPage(
        await api.get<unknown>(`/api/sites/${encodeURIComponent(siteId)}/events`, {
          query: { ap: filters.ap, client: filters.client, kind: filters.kind, severity: filters.severity, since, before: pageParam || undefined, limit },
          signal,
        }),
      )
    },
    initialPageParam: before ?? '',
    getNextPageParam: (last) => last.next_before ?? undefined,
    staleTime: stale.live,
  })

export const eventQuery = (siteId: string, eventId: string) =>
  queryOptions({
    queryKey: [...keys.site(siteId), 'event', eventId] as const,
    queryFn: ({ signal }) => api.get<EventView>(`/api/sites/${encodeURIComponent(siteId)}/events/${encodeURIComponent(eventId)}`, { signal }),
    staleTime: stale.live,
  })

/** Overview: the newest events (§3.3); the same stream-patched default list as the Events page. */
export const recentEventsQuery = (siteId: string) => siteEventsQuery(siteId, {})
