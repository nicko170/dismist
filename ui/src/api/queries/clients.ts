import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { ClientDetail, ClientView, MutationResponse, SessionView } from '@/gen/api.gen'
import { isPrivateMac, normaliseMac } from '@/lib/mac'

// Site clients (design §3.12, §4.9; workstream E): one request per site,
// refreshed every 10 s while the page is visible; recent sessions from the
// time-ordered index. Filters apply client-side to the one response.

/** Client metadata (phase 4, `PUT /api/clients/$mac/meta`). */
export type ClientMeta = { alias: string; notes: string }

const str = (value: unknown, fallback = ''): string => (typeof value === 'string' ? value : fallback)
const num = (value: unknown, fallback = 0): number => (typeof value === 'number' && Number.isFinite(value) ? value : fallback)
const numOrNull = (value: unknown): number | null => (typeof value === 'number' && Number.isFinite(value) ? value : null)

function signalOf(rssi: number): ClientView['signal'] {
  if (rssi >= -60) return 'excellent'
  if (rssi >= -67) return 'good'
  if (rssi >= -75) return 'fair'
  return 'poor'
}

/** v1 ClientView, plus the pre-v1 `{clients: [{...telemetry client, ap_id, ap_name}]}` rows. */
export function normaliseClient(raw: Record<string, unknown>): ClientView {
  const mac = normaliseMac(str(raw.mac)) ?? str(raw.mac)
  const rssi = num(raw.rssi)
  const ip = str(raw.ip)
  const hostname = str(raw.hostname)
  const alias = typeof raw.alias === 'string' && raw.alias ? raw.alias : null
  return {
    mac,
    private_mac: typeof raw.private_mac === 'boolean' ? raw.private_mac : isPrivateMac(mac),
    name: str(raw.name) || alias || hostname || mac,
    hostname,
    alias,
    ip,
    self_assigned: typeof raw.self_assigned === 'boolean' ? raw.self_assigned : ip.startsWith('169.254.'),
    ipv6: Array.isArray(raw.ipv6) ? raw.ipv6.filter((v): v is string => typeof v === 'string') : [],
    ap_id: str(raw.ap_id),
    ap_name: str(raw.ap_name),
    wlan_id: str(raw.wlan_id),
    ssid: str(raw.ssid),
    band: str(raw.band),
    channel: num(raw.channel),
    width: num(raw.width),
    bssid: str(raw.bssid),
    protocol: str(raw.protocol),
    streams: numOrNull(raw.streams),
    rssi,
    avg_rssi: num(raw.avg_rssi, num(raw.average_rssi)),
    snr: numOrNull(raw.snr),
    signal: (['excellent', 'good', 'fair', 'poor'] as const).includes(raw.signal as ClientView['signal'])
      ? (raw.signal as ClientView['signal'])
      : signalOf(rssi),
    tx_rate_mbps: numOrNull(raw.tx_rate_mbps),
    rx_rate_mbps: numOrNull(raw.rx_rate_mbps),
    down_bytes: num(raw.down_bytes, num(raw.tx_bytes)),
    up_bytes: num(raw.up_bytes, num(raw.rx_bytes)),
    connected_seconds: num(raw.connected_seconds, num(raw.uptime_seconds)),
    idle_ms: num(raw.idle_ms, num(raw.inactive_millis)),
  }
}

export function normaliseClients(body: unknown): ClientView[] {
  const rows = Array.isArray(body) ? body : ((body as { clients?: unknown[] } | null)?.clients ?? [])
  return rows
    .filter((row): row is Record<string, unknown> => typeof row === 'object' && row !== null && typeof (row as { mac?: unknown }).mac === 'string')
    .map(normaliseClient)
}

export function normaliseSessions(body: unknown): SessionView[] {
  const rows = Array.isArray(body) ? body : ((body as { sessions?: unknown[] } | null)?.sessions ?? [])
  return rows
    .filter((row): row is Record<string, unknown> => typeof row === 'object' && row !== null && typeof (row as { mac?: unknown }).mac === 'string')
    .map((row) => ({
      site_id: str(row.site_id),
      ap_id: str(row.ap_id),
      ap_name: str(row.ap_name),
      mac: normaliseMac(str(row.mac)) ?? str(row.mac),
      ssid: str(row.ssid),
      ip: str(row.ip),
      vlan_id: num(row.vlan_id),
      connected_at: str(row.connected_at),
      last_seen: str(row.last_seen),
      disconnected_at: str(row.disconnected_at) && !str(row.disconnected_at).startsWith('0001-') ? str(row.disconnected_at) : null,
      end_reason: str(row.end_reason),
      rssi: num(row.rssi),
      down_bytes: num(row.down_bytes, num(row.tx_bytes)),
      up_bytes: num(row.up_bytes, num(row.rx_bytes)),
    }))
}

/** GET /api/sites/$id/clients?view=live, every 10 s while visible (§3.12, §7.5). */
export const siteClientsQuery = (siteId: string) =>
  queryOptions({
    queryKey: keys.siteClients(siteId, 'live'),
    queryFn: async ({ signal }) =>
      normaliseClients(await api.get<unknown>(`/api/sites/${encodeURIComponent(siteId)}/clients`, { query: { view: 'live' }, signal })),
    staleTime: stale.live,
    refetchInterval: 10_000,
    refetchOnWindowFocus: true,
  })

/** GET /api/sites/$id/clients?view=recent: sessions ended in the last 24 h. */
export const siteRecentQuery = (siteId: string) =>
  queryOptions({
    queryKey: keys.siteClients(siteId, 'recent'),
    queryFn: async ({ signal }) =>
      normaliseSessions(await api.get<unknown>(`/api/sites/${encodeURIComponent(siteId)}/clients`, { query: { view: 'recent' }, signal })),
    staleTime: stale.live,
    refetchInterval: 30_000,
  })

export function normaliseClientDetail(body: unknown): ClientDetail {
  const row = (body ?? {}) as Record<string, unknown>
  const live = row.live && typeof row.live === 'object' ? normaliseClient(row.live as Record<string, unknown>) : null
  return {
    site_id: str(row.site_id),
    live,
    per_antenna_rssi: Array.isArray(row.per_antenna_rssi) ? row.per_antenna_rssi.filter((v): v is number => typeof v === 'number') : [],
    username: str(row.username),
    user_agent: str(row.user_agent),
    retries: { tx: num((row.retries as ClientDetail['retries'] | undefined)?.tx), rx: num((row.retries as ClientDetail['retries'] | undefined)?.rx) },
    qos: Array.isArray(row.qos) ? (row.qos as ClientDetail['qos']) : [],
    sessions: normaliseSessions(row.sessions ?? []),
    events: Array.isArray(row.events) ? (row.events as ClientDetail['events']) : [],
    next_before: typeof row.next_before === 'string' ? row.next_before : null,
    meta:
      row.meta && typeof row.meta === 'object'
        ? { mac: str((row.meta as Record<string, unknown>).mac), alias: str((row.meta as Record<string, unknown>).alias), notes: str((row.meta as Record<string, unknown>).notes) }
        : null,
  }
}

/** GET /api/clients/$mac?site=: live record, sessions and events. */
export const clientQuery = (mac: string, siteId?: string) =>
  queryOptions({
    queryKey: keys.client(mac),
    queryFn: async ({ signal }) =>
      normaliseClientDetail(await api.get<unknown>(`/api/clients/${encodeURIComponent(mac)}`, { query: { site: siteId }, signal })),
    staleTime: stale.live,
    refetchInterval: 10_000,
  })

/** Client meta (alias, notes; phase 4): ClientDetail.meta, null when none is stored. */
export function metaOf(detail: ClientDetail | undefined): ClientMeta {
  const stored = detail?.meta
  return { alias: stored?.alias ?? detail?.live?.alias ?? '', notes: stored?.notes ?? '' }
}

/** POST …/clients/$mac/block|unblock: a config change (site blocked clients), so dry run first. */
export function useBlockClient(siteId: string, mac: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ block, dryRun }: { block: boolean; dryRun?: boolean }) =>
      api.post<MutationResponse<unknown>>(
        `/api/sites/${encodeURIComponent(siteId)}/clients/${encodeURIComponent(mac)}/${block ? 'block' : 'unblock'}`,
        {},
        { query: { dry_run: dryRun ? 1 : undefined } },
      ),
    onSuccess: (_r, { dryRun }) => {
      if (dryRun) return
      void qc.invalidateQueries({ queryKey: keys.client(mac) })
      void qc.invalidateQueries({ queryKey: keys.site(siteId) })
    },
  })
}

/** POST …/clients/$mac/disconnect: gated like reboot (§5.6). */
export function useDisconnectClient(siteId: string, mac: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<unknown>(`/api/sites/${encodeURIComponent(siteId)}/clients/${encodeURIComponent(mac)}/disconnect`, {}),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.siteClients(siteId, 'live') }),
  })
}

/** PUT /api/clients/$mac/meta {alias, notes} (phase 4). */
export function useClientMeta(mac: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (meta: ClientMeta) => api.put<unknown>(`/api/clients/${encodeURIComponent(mac)}/meta`, meta),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.client(mac) })
      void qc.invalidateQueries({ queryKey: ['site'], predicate: (q) => q.queryKey[2] === 'clients' })
    },
  })
}
