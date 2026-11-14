// PROVISIONAL contract types for phases 0 and 1, written by hand from design
// §4.9 and §4.10 until workstream C lands `src/gen/api.gen.ts` (tygo over
// internal/api). When it lands, replace each declaration here with a
// re-export from '@/gen/api.gen' (or delete this file and update imports);
// nobody should add a second copy of a shape that the generator emits.

// Every code the server sends, generated from internal/api/errors.go.
import type { ErrorCode } from '@/gen/api.gen'
export type { ErrorCode }

/** Every non-2xx response body (design §4.8). */
export type ErrorBody = {
  error: string
  code: ErrorCode | (string & {})
  fields?: Record<string, string>
}

export type AuthSession = { authenticated: boolean; expires_at?: string | null }

export type Band = '2.4' | '5'

export type APStatus =
  | 'auth_failing'
  | 'source_mismatch'
  | 'config_error'
  | 'never_connected'
  | 'offline'
  | 'rejected'
  | 'pending'
  | 'applying'
  | 'in_sync'

export type RadioSummary = { band: Band; channel: number; width: number; clients: number; util: number }

export type APView = {
  id: string
  name: string
  site_id: string
  model: string
  model_source: 'ap' | 'record'
  firmware: string
  firmware_tested: boolean
  status: APStatus
  flags: string[]
  connected: boolean
  connected_at: string | null
  last_seen: string | null
  lan_ip: string
  remote_address: string
  behind_nat: boolean
  pinned_ip: string
  has_secret: boolean
  config_revision: number
  applied_revision: number
  last_error: string
  client_count: number
  uptime_seconds: number
  radios: RadioSummary[]
  updated_rev: number
}

/** APDetail adds identity, connection, health, uplink and more (§4.9); phase 1 fills it in. */
export type APDetail = APView & {
  identity: {
    serial: string
    sku: string
    boot_count: number
    last_reboot_reason: string
    last_reboot_time: string | null
    last_boot_state: string
    tunnel_ipv6: string
  }
  connection: {
    connection_count: number
    last_disconnect: string | null
    tls_cipher: string
    rtt_ms: number | null
    pmtu: number | null
    push_mode: 'notify' | 'reconnect'
    clock_skew_s: number | null
  }
  health: {
    cpu_temp_c: number | null
    cpu_busy: number | null
    mem_total_bytes: number
    mem_available_bytes: number
    load: [number, number, number]
    rw_total_bytes: number
    rw_available_bytes: number
    agent_heap_bytes: number | null
  }
  locate_until: string | null
  [extra: string]: unknown
}

export type SiteCounts = { aps: number; aps_online: number; clients: number; wlans: number; pending_aps: number }

/** GET /api/sites row: the SiteView header plus counts (§3.2). */
export type SiteSummary = {
  id: string
  name: string
  country: string
  timezone: string
  counts: SiteCounts
}

export type SiteView = SiteSummary & {
  ccrev: string | null
  experimental: boolean
  updated_rev: number
  [extra: string]: unknown
}

export type Severity = 'info' | 'warning' | 'error'

export type EventView = {
  id: string
  site_id: string
  ap_id: string
  ap_name: string
  kind: string
  pace_type: number | null
  severity: Severity
  title: string
  detail: string
  client_mac: string
  wlan_id: string
  first_seen: string
  last_seen: string
  count: number
  resolved_by: string | null
  data: Record<string, unknown>
}

/** One page of GET /api/sites/$id/events. */
export type EventPage = { events: EventView[]; next_before: string | null }

export type PendingAP = {
  id: string
  model: string
  sku: string
  serial: string
  firmware: string
  local_ip: string
  remote_address: string
  first_seen: string
  last_seen: string
  attempts: number
  reason: 'unregistered' | 'source_mismatch' | 'auth_failed'
}

export type SystemView = {
  version: string
  commit: string
  go: string
  started_at: string
  ready: boolean
  wss: {
    url: string
    listen: string
    cert: { subject: string; sans: string[]; issuer: string; not_after: string }
    chain: { subject: string; not_after: string; role: 'intermediate' | 'root' }[]
    sessions: number
  }
  dns_mode: boolean
  trusted_proxies: string[]
  db: {
    path: string
    size_bytes: number
    free_bytes: number
    schema: number
    compat: number
    rollback_without_restore: boolean
    retention: Record<string, string>
  }
  backup: {
    last_success: string | null
    next: string | null
    keep: number
    max_bytes: number
    used_bytes: number
    dir: string
    pre_migration: { name: string; size_bytes: number }[]
  }
  metrics: { listen: string; remote: boolean; names: boolean; clients: 'off' | 'aggregate' | 'per-client' }
  migration: { state: 'not_run' | 'committed' | 'legacy'; reason: string; report_available: boolean }
}

export type OnboardingView = {
  wss_url: string
  dns_mode: boolean
  ca_pems: { name: string; pem: string }[]
  cloud_ini: string
}

export type TokenView = { id: string; name: string; scope: 'read' | 'admin'; created: string; last_used: string | null }
export type TokenCreated = TokenView & { token: string }

export type ClientView = {
  mac: string
  private_mac: boolean
  name: string
  hostname: string
  alias: string | null
  ip: string
  self_assigned: boolean
  ipv6: string[]
  ap_id: string
  ap_name: string
  wlan_id: string
  ssid: string
  band: string
  channel: number
  width: number
  bssid: string
  protocol: string
  streams: number | null
  rssi: number
  avg_rssi: number
  snr: number | null
  signal: 'excellent' | 'good' | 'fair' | 'poor'
  tx_rate_mbps: number | null
  rx_rate_mbps: number | null
  down_bytes: number
  up_bytes: number
  connected_seconds: number
  idle_ms: number
}

export type MutationResponse<T> = {
  revision: number
  object: T
  impact: {
    aps: {
      id: string
      name: string
      connected: boolean
      push: 'notify' | 'reconnect'
      disruption: string[]
      diff?: { path: string; from: unknown; to: unknown }[]
    }[]
    disruption: string
    summary: string
  }
  warnings: { code: string; message: string }[]
  dry_run?: boolean
}

// ---- SSE (§4.10) ----

export type APLive = {
  id: string
  status: APStatus
  connected: boolean
  applied_revision: number
  config_revision: number
  last_error: string
  client_count: number
  uptime_seconds: number
  lan_ip: string
  remote_address: string
  radios: RadioSummary[]
  seen_at: string
}

export type StreamSnapshot = { site_id: string; revision: number; aps: APLive[] }

export type EventSummary = Omit<EventView, 'client_mac' | 'wlan_id' | 'data' | 'site_id' | 'ap_name' | 'detail' | 'pace_type'> & {
  has_client: boolean
}

export type ConfigEvent = {
  revision: number
  kind: string
  object_id: string
  affected_aps: string[]
  actor: unknown
}

export type PushState = 'notified' | 'fetched' | 'applied' | 'rejected' | 'fallback_reconnect' | 'timeout'
export type PushEvent = { ap_id: string; revision: number; state: PushState; error: string }

export type PendingEvent = { count: number }

export type StreamMessage =
  | { type: 'snapshot'; data: StreamSnapshot }
  | { type: 'ap'; data: APLive }
  | { type: 'event'; data: EventSummary }
  | { type: 'config'; data: ConfigEvent }
  | { type: 'push'; data: PushEvent }
  | { type: 'pending'; data: PendingEvent }
