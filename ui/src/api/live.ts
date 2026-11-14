import { queryOptions } from '@tanstack/react-query'
import { api } from './client'
import { keys, stale } from './keys'
import type { APStatus, APView, PendingAP } from './types'

// The live lists the site stream patches in place (design §7.6): the site's
// AP list and the pending-AP count. The site layout keeps the AP list
// subscribed, so `snapshot` and `ap` events always have something to patch
// and the shell's offline badge and title stay current on every screen.
// Workstream E's api/queries/aps.ts builds its AP queries on these.

const APStatuses: readonly APStatus[] = [
  'auth_failing',
  'source_mismatch',
  'config_error',
  'never_connected',
  'offline',
  'rejected',
  'pending',
  'applying',
  'in_sync',
]

/**
 * Status for a pre-v1 AP record, which has no `status` (design §3.4 order,
 * limited to what the old view carries).
 */
export function deriveStatus(ap: { connected: boolean; last_seen?: string | null; applied_revision: number; config_revision: number; last_error?: string }): APStatus {
  if (!ap.connected) return ap.last_seen && !ap.last_seen.startsWith('0001-') ? 'offline' : 'never_connected'
  if (ap.last_error) return 'rejected'
  if (ap.config_revision && ap.applied_revision < ap.config_revision) return 'applying'
  return 'in_sync'
}

const str = (value: unknown, fallback = ''): string => (typeof value === 'string' ? value : fallback)
const num = (value: unknown, fallback = 0): number => (typeof value === 'number' && Number.isFinite(value) ? value : fallback)
const nullableTime = (value: unknown): string | null => {
  const text = str(value)
  return text && !text.startsWith('0001-') ? text : null
}

/**
 * Accepts the v1 `[APView]` and the pre-v1 `{aps: [...]}` (fewer fields, no
 * status), so the shell shows live AP state against either server.
 */
export function normaliseAPs(body: unknown): APView[] {
  const rows = Array.isArray(body) ? body : ((body as { aps?: unknown[] } | null)?.aps ?? [])
  return rows
    .filter((row): row is Record<string, unknown> => typeof row === 'object' && row !== null && typeof row.id === 'string')
    .map((row) => {
      const connected = Boolean(row.connected)
      const lanIP = str(row.lan_ip)
      const remote = str(row.remote_address)
      const view: APView = {
        id: row.id as string,
        name: str(row.name) || (row.id as string),
        site_id: str(row.site_id),
        model: str(row.model),
        model_source: row.model_source === 'record' ? 'record' : 'ap',
        firmware: str(row.firmware),
        firmware_tested: Boolean(row.firmware_tested),
        status: 'in_sync',
        flags: Array.isArray(row.flags) ? row.flags.filter((flag): flag is string => typeof flag === 'string') : [],
        connected,
        connected_at: nullableTime(row.connected_at),
        last_seen: nullableTime(row.last_seen),
        lan_ip: lanIP,
        remote_address: remote,
        behind_nat: typeof row.behind_nat === 'boolean' ? row.behind_nat : Boolean(remote && lanIP && remote !== lanIP),
        pinned_ip: str(row.pinned_ip, str(row.management_ip)),
        has_secret: Boolean(row.has_secret),
        config_revision: num(row.config_revision),
        applied_revision: num(row.applied_revision),
        last_error: str(row.last_error),
        client_count: num(row.client_count),
        uptime_seconds: num(row.uptime_seconds),
        radios: Array.isArray(row.radios) ? (row.radios as APView['radios']) : [],
        updated_rev: num(row.updated_rev),
      }
      view.status = APStatuses.includes(row.status as APStatus) ? (row.status as APStatus) : deriveStatus(view)
      return view
    })
}

/** GET /api/aps?site= (design §4.9): the site's AP list, patched by the stream. */
export const siteAPsQuery = (siteId: string) =>
  queryOptions({
    queryKey: keys.siteAPs(siteId),
    queryFn: async ({ signal }) => normaliseAPs(await api.get<unknown>('/api/aps', { query: { site: siteId }, signal })),
    staleTime: stale.live,
  })

/**
 * The pending-AP count for the "Add an AP" badge. The `pending` stream event
 * sets it; this query seeds it. A server without the endpoint counts as 0.
 */
export const pendingCountQuery = queryOptions({
  queryKey: keys.pendingCount,
  queryFn: async ({ signal }) => {
    try {
      const rows = await api.get<PendingAP[] | { pending?: PendingAP[] }>('/api/aps/pending', { signal })
      return Array.isArray(rows) ? rows.length : (rows?.pending?.length ?? 0)
    } catch {
      return 0
    }
  },
  staleTime: stale.config,
})
