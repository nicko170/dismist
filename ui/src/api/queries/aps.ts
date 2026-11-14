import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { APDetail, APStatus, APView, MutationResponse, PendingAP } from '@/gen/api.gen'

// AP reads and actions for the monitoring screens (design §3.4, §3.5, §4.9;
// workstream E). Lists come from one request per site and are patched by the
// site stream (§7.6); nothing here fetches per AP for a list.

// ---- provisional contract shapes (design §4.9 "Remaining shapes"; not yet
// emitted by tygo). Replace with '@/gen/api.gen' imports when C adds them. ----

/** POST /api/aps/$id/actions/$action response. */
export type ActionResult = { action: string; state: 'accepted' | 'done'; until?: string; action_id?: string }
export type APAction = 'locate' | 'unlocate' | 'push-config' | 'reboot' | 'restart-radios'

/** GET /api/sites/$id/rf/neighbours (and ?ap= for one AP's scan), design §5.10. */
export type Neighbour = {
  ssid: string
  bssid: string
  band: string
  channel: number
  width: number
  rssi: number
  seen_by: string
  station_count: number | null
  util: number | null
  last_seen: string
}

/** One history sample (design §4.1 `samples`): per AP per minute, or aggregated per step. */
export type SamplePoint = {
  at: number
  ap_id: string
  connected: boolean
  client_count: number
  radios: { band: string; channel: number; width: number; util: number; clients: number; max_power_dbm: number | null }[]
}

/** One line of the AP kernel-log ring (field 24.1, phase 4). */
export type KernelLogLine = { at: string | null; line: string }

// ---- keys (structurally identical to WS-D's keys.ts builders on p1/d-ui) ----

export const apKeys = {
  clients: (apId: string) => [...keys.ap(apId), 'clients'] as const,
  telemetry: (apId: string) => keys.apTelemetry(apId),
  neighbours: (siteId: string, apId?: string) => [...keys.site(siteId), 'neighbours', apId ?? ''] as const,
  samples: (siteId: string, params: { ap?: string; range?: string; step?: string }) =>
    [...keys.site(siteId), 'samples', params] as const,
  pendingList: (dismissed = false) => [...keys.pending, 'list', { dismissed }] as const,
}

// ---- normalisers (the v1 shapes, plus the pre-v1 `{aps: [...]}` envelope) ----

const STATUSES: readonly APStatus[] = [
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

const str = (value: unknown, fallback = ''): string => (typeof value === 'string' ? value : fallback)
const num = (value: unknown, fallback = 0): number => (typeof value === 'number' && Number.isFinite(value) ? value : fallback)
const time = (value: unknown): string | null => {
  const text = str(value)
  return text && !text.startsWith('0001-') ? text : null
}

/** Status for a pre-v1 record with no `status` (the §3.4 order, limited to what it carries). */
export function deriveStatus(ap: Pick<APView, 'connected' | 'last_seen' | 'applied_revision' | 'config_revision' | 'last_error'>): APStatus {
  if (!ap.connected) return ap.last_seen ? 'offline' : 'never_connected'
  if (ap.last_error) return 'rejected'
  if (ap.config_revision && ap.applied_revision < ap.config_revision) return 'applying'
  return 'in_sync'
}

export function normaliseAP(row: Record<string, unknown>): APView {
  const lanIP = str(row.lan_ip)
  const remote = str(row.remote_address)
  const view: APView = {
    id: str(row.id),
    name: str(row.name) || str(row.id),
    site_id: str(row.site_id),
    model: str(row.model),
    model_source: row.model_source === 'record' ? 'record' : 'ap',
    firmware: str(row.firmware),
    firmware_tested: typeof row.firmware_tested === 'boolean' ? row.firmware_tested : str(row.firmware) === 'apfw-0.15.35064-noro-84df',
    status: 'in_sync',
    flags: Array.isArray(row.flags) ? row.flags.filter((f): f is string => typeof f === 'string') : [],
    connected: Boolean(row.connected),
    connected_at: time(row.connected_at),
    last_seen: time(row.last_seen),
    lan_ip: lanIP,
    remote_address: remote,
    behind_nat: typeof row.behind_nat === 'boolean' ? row.behind_nat : Boolean(remote && lanIP && remote.split(':')[0] !== lanIP),
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
  view.status = STATUSES.includes(row.status as APStatus) ? (row.status as APStatus) : deriveStatus(view)
  return view
}

export function normaliseAPs(body: unknown): APView[] {
  const rows = Array.isArray(body) ? body : ((body as { aps?: unknown[] } | null)?.aps ?? [])
  return rows
    .filter((row): row is Record<string, unknown> => typeof row === 'object' && row !== null && typeof (row as { id?: unknown }).id === 'string')
    .map(normaliseAP)
}

/** APDetail with every section present, so tabs never crash on a partial (older) server. */
export function normaliseDetail(body: unknown): APDetail {
  const row = (body ?? {}) as Record<string, unknown>
  const base = normaliseAP(row)
  const obj = <T>(value: unknown, fallback: T): T => (value && typeof value === 'object' ? ({ ...fallback, ...(value as object) } as T) : fallback)
  return {
    ...base,
    identity: obj(row.identity, { serial: '', sku: '', boot_count: 0, last_reboot_reason: '', last_reboot_time: null, last_boot_state: '', tunnel_ipv6: '' }),
    connection: obj(row.connection, {
      connection_count: num(row.connection_count),
      last_disconnect: null,
      tls_cipher: str(row.tls_cipher_suite),
      rtt_ms: null,
      pmtu: null,
      push_mode: 'reconnect' as const,
      clock_skew_s: null,
    }),
    config: obj(row.config, {
      effective_hash: '',
      applied_timestamp: 0,
      desired_timestamp: 0,
      recovered_from_bad_config: false,
      good_5min: false,
      shadowed: [],
      compile_error: '',
      last_verification: null,
      country_reported: '',
    }),
    protocol_features: obj(row.protocol_features, {}),
    local_files: Array.isArray(row.local_files) ? (row.local_files as APDetail['local_files']) : null,
    health: obj(row.health, {
      cpu_temp_c: null,
      cpu_busy: null,
      mem_total_bytes: 0,
      mem_available_bytes: 0,
      load: [0, 0, 0] as [number, number, number],
      rw_total_bytes: 0,
      rw_available_bytes: 0,
      agent_heap_bytes: null,
    }),
    uplink: obj(row.uplink, { ports: [], lldp: [], power: null, inactive_vlans: [] }),
    ip: obj(row.ip, {
      vlan: '',
      dhcp_state: '',
      address: '',
      prefix: 0,
      renewed_at: null,
      lease_seconds: 0,
      gateway: '',
      gateway_mac: '',
      dns: [],
      dhcp_server: '',
      domain: '',
      ntp: [],
      dad_failed: [],
    }),
    radios_detail: Array.isArray(row.radios_detail) ? (row.radios_detail as APDetail['radios_detail']) : [],
    wlans_live: Array.isArray(row.wlans_live) ? (row.wlans_live as APDetail['wlans_live']) : [],
    overrides: obj(row.overrides, { led_brightness: null, ssh_enabled: null, rf: { '2.4': null, '5': null }, management: null, eth1: null }),
    rf_plan: obj(row.rf_plan, { '2.4': null, '5': null }),
    locate_until: time(row.locate_until),
    capabilities: obj(row.capabilities, { config_change_notify: null, ap_cmd: false, pong2: false }),
    commands: obj(row.commands, { reboot: false, restart_radios: false, disconnect: false, locate: false, fallback: '' }),
    reboots_detected: Array.isArray(row.reboots_detected) ? row.reboots_detected.filter((at): at is string => typeof at === 'string') : [],
  }
}

export function normalisePending(body: unknown): PendingAP[] {
  const rows = Array.isArray(body) ? body : ((body as { pending?: unknown[] } | null)?.pending ?? [])
  return rows.filter((row): row is PendingAP => typeof row === 'object' && row !== null && typeof (row as PendingAP).id === 'string')
}

/** Accepts `{samples: [...]}` or an array; minute samples or step aggregates. */
export function normaliseSamples(body: unknown): SamplePoint[] {
  const rows = Array.isArray(body) ? body : ((body as { samples?: unknown[] } | null)?.samples ?? [])
  const out: SamplePoint[] = []
  for (const raw of rows) {
    if (!raw || typeof raw !== 'object') continue
    const row = raw as Record<string, unknown>
    const at = Date.parse(str(row.at) || str(row.minute) || str(row.observed_at))
    if (Number.isNaN(at)) continue
    out.push({
      at,
      ap_id: str(row.ap_id),
      connected: row.connected === undefined ? true : Boolean(row.connected),
      client_count: num(row.client_count, num(row.clients)),
      radios: (Array.isArray(row.radios) ? row.radios : []).map((r) => {
        const radio = (r ?? {}) as Record<string, unknown>
        return {
          band: str(radio.band),
          channel: num(radio.channel),
          width: num(radio.width),
          util: num(radio.util, num(radio.util_all)),
          clients: num(radio.clients),
          max_power_dbm: typeof radio.max_power_dbm === 'number' ? radio.max_power_dbm : null,
        }
      }),
    })
  }
  return out.toSorted((a, b) => a.at - b.at)
}

export const RANGES = { '1h': 3600_000, '24h': 86400_000, '7d': 7 * 86400_000 } as const
export type HistoryRange = keyof typeof RANGES
export const STEPS: Record<HistoryRange, string> = { '1h': '1m', '24h': '10m', '7d': '1h' }
const STEP_MS: Record<HistoryRange, number> = { '1h': 60_000, '24h': 600_000, '7d': 3600_000 }

/**
 * The site's client count per step: within a step each AP contributes its
 * highest count, then APs are summed. Steps with no sample at all are gaps
 * (null), never zeros, so an outage is not drawn as "0 clients".
 */
export function siteClientSeries(samples: SamplePoint[], range: HistoryRange, now = Date.now()): { t: number; v: number | null }[] {
  const step = STEP_MS[range]
  const start = Math.floor((now - RANGES[range]) / step) * step
  const buckets = new Map<number, Map<string, number>>()
  for (const sample of samples) {
    if (sample.at < start) continue
    const bucket = Math.floor(sample.at / step) * step
    const perAP = buckets.get(bucket) ?? new Map<string, number>()
    perAP.set(sample.ap_id, Math.max(perAP.get(sample.ap_id) ?? 0, sample.client_count))
    buckets.set(bucket, perAP)
  }
  const points: { t: number; v: number | null }[] = []
  for (let t = start; t <= now; t += step) {
    const perAP = buckets.get(t)
    points.push({ t, v: perAP ? [...perAP.values()].reduce((a, b) => a + b, 0) : null })
  }
  return points
}

// ---- queries ----

/** GET /api/aps?site= (shares the key the stream patches). */
export const siteAPsQuery = (siteId: string) =>
  queryOptions({
    queryKey: keys.siteAPs(siteId),
    queryFn: async ({ signal }) => normaliseAPs(await api.get<unknown>('/api/aps', { query: { site: siteId }, signal })),
    staleTime: stale.live,
  })

/** GET /api/aps (every site): the stable /aps/$apId link finds an AP's site here when the detail is missing. */
export const allAPsQuery = queryOptions({
  queryKey: ['aps', 'all'] as const,
  queryFn: async ({ signal }) => normaliseAPs(await api.get<unknown>('/api/aps', { signal })),
  staleTime: stale.live,
})

/** GET /api/aps/$id: APDetail, patched by `ap` stream events. */
export const apQuery = (apId: string) =>
  queryOptions({
    queryKey: keys.ap(apId),
    queryFn: async ({ signal }) => normaliseDetail(await api.get<unknown>(`/api/aps/${encodeURIComponent(apId)}`, { signal })),
    staleTime: stale.live,
  })

/** GET /api/aps/$id/telemetry: only the phase-4 kernel log ring is read from it. */
export const apKernelLogQuery = (apId: string) =>
  queryOptions({
    queryKey: [...apKeys.telemetry(apId), 'kernel_log'] as const,
    queryFn: async ({ signal }) => {
      const body = await api.get<{ kernel_log?: unknown }>(`/api/aps/${encodeURIComponent(apId)}/telemetry`, { signal })
      const rows = Array.isArray(body?.kernel_log) ? body.kernel_log : []
      return rows.map((row): KernelLogLine =>
        typeof row === 'string' ? { at: null, line: row } : { at: time((row as KernelLogLine).at), line: str((row as KernelLogLine).line) },
      )
    },
    staleTime: stale.live,
  })

export const pendingQuery = (dismissed = false) =>
  queryOptions({
    queryKey: apKeys.pendingList(dismissed),
    queryFn: async ({ signal }) => normalisePending(await api.get<unknown>('/api/aps/pending', { query: { dismissed: dismissed ? 1 : undefined }, signal })),
    staleTime: stale.live,
  })

export const samplesQuery = (siteId: string, params: { ap?: string; range: HistoryRange }) =>
  queryOptions({
    queryKey: apKeys.samples(siteId, { ap: params.ap, range: params.range, step: STEPS[params.range] }),
    queryFn: async ({ signal }) =>
      normaliseSamples(
        await api.get<unknown>(`/api/sites/${encodeURIComponent(siteId)}/history/samples`, {
          query: { ap: params.ap, range: params.range, step: STEPS[params.range] },
          signal,
        }),
      ),
    staleTime: 60_000,
    refetchInterval: 60_000,
  })

export function normaliseNeighbours(body: unknown): Neighbour[] {
  const rows = Array.isArray(body) ? body : ((body as { neighbours?: unknown[] } | null)?.neighbours ?? [])
  return rows
    .filter((row): row is Neighbour => typeof row === 'object' && row !== null && typeof (row as Neighbour).bssid === 'string')
    .toSorted((a, b) => b.rssi - a.rssi)
}

/** Neighbours heard by one AP's scans (phase 3). */
export const neighboursQuery = (siteId: string, apId?: string, band?: string) =>
  queryOptions({
    queryKey: [...apKeys.neighbours(siteId, apId), band ?? ''] as const,
    queryFn: async ({ signal }) =>
      normaliseNeighbours(
        await api.get<unknown>(`/api/sites/${encodeURIComponent(siteId)}/rf/neighbours`, { query: { ap: apId, band }, signal }),
      ),
    staleTime: 60_000,
  })

// ---- mutations ----

/** DELETE /api/aps/pending/$id?days=30: hides the row; attempts keep counting. */
export function useDismissPending() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, days = 30 }: { id: string; days?: number }) =>
      api.delete<void>(`/api/aps/pending/${encodeURIComponent(id)}`, { query: { days } }),
    onSuccess: (_data, { id }) => {
      qc.setQueryData<PendingAP[]>(apKeys.pendingList(false), (rows) => rows?.filter((row) => row.id !== id))
      qc.setQueryData<number>(keys.pendingCount, (count) => (typeof count === 'number' ? Math.max(0, count - 1) : count))
      void qc.invalidateQueries({ queryKey: keys.pending })
    },
  })
}

/** POST /api/aps/$id/actions/$action (locate {minutes}, restart-radios {band}, …). */
export function useAPAction(apId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ action, body }: { action: APAction; body?: Record<string, unknown> }) =>
      api.post<ActionResult>(`/api/aps/${encodeURIComponent(apId)}/actions/${action}`, body ?? {}),
    onSuccess: (result, { action }) => {
      if (action === 'locate' || action === 'unlocate') {
        qc.setQueryData<APDetail>(keys.ap(apId), (detail) =>
          detail ? { ...detail, locate_until: action === 'locate' ? (result?.until ?? detail.locate_until) : null } : detail,
        )
      }
      void qc.invalidateQueries({ queryKey: keys.ap(apId), exact: true })
    },
  })
}

/** PATCH /api/aps/$id {site_id}: move an AP (dry run first for the impact line). */
export function useMoveAP(apId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ siteId, rev, dryRun }: { siteId: string; rev: number; dryRun?: boolean }) =>
      api.patch<MutationResponse<APView>>(`/api/aps/${encodeURIComponent(apId)}`, { site_id: siteId }, {
        ifMatch: rev,
        query: { dry_run: dryRun ? 1 : undefined },
      }),
    onSuccess: (_result, { dryRun }) => {
      if (dryRun) return
      void qc.invalidateQueries({ queryKey: ['site'] })
      void qc.invalidateQueries({ queryKey: keys.ap(apId) })
      void qc.invalidateQueries({ queryKey: keys.sites })
    },
  })
}

/** DELETE /api/aps/$id[?purge_history=1]: closes the session and deletes the record and secret. */
export function useDeleteAP(apId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ rev, purge }: { rev: number; purge: boolean }) =>
      api.delete<void>(`/api/aps/${encodeURIComponent(apId)}`, { ifMatch: rev, query: { purge_history: purge ? 1 : undefined } }),
    onSuccess: () => {
      qc.removeQueries({ queryKey: keys.ap(apId) })
      void qc.invalidateQueries({ queryKey: ['site'] })
      void qc.invalidateQueries({ queryKey: keys.sites })
    },
  })
}
