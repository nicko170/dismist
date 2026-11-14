import type {
  APDetail,
  APView,
  ClientView,
  EventView,
  OnboardingView,
  PendingAP,
  SiteSummary,
  SiteView,
  SystemView,
  TokenView,
} from '../types'

// Synthetic fixtures for the phase-0/1 shapes of design §4.9. Documentation
// addresses only (RFC 5737 192.0.2.0/24 and 198.51.100.0/24), locally
// administered MACs (02-aa-…), SSID "Example". Nothing here is real.

export const MOCK_ADMIN_TOKEN = 'a1'.repeat(32)
export const MOCK_API_TOKEN = 'dsm_mock_0123456789abcdef'

const now = Date.now()
const ago = (seconds: number) => new Date(now - seconds * 1000).toISOString()

export const sites: SiteSummary[] = [
  {
    id: 'home',
    name: 'Home',
    country: 'AU',
    timezone: 'Australia/Sydney',
    counts: { aps: 3, aps_online: 2, clients: 19, wlans: 2, pending_aps: 1 },
  },
  {
    id: 'lab',
    name: 'Lab',
    country: 'AU',
    timezone: 'Australia/Sydney',
    counts: { aps: 1, aps_online: 1, clients: 2, wlans: 1, pending_aps: 0 },
  },
]

export function siteView(summary: SiteSummary): SiteView {
  return { ...summary, ccrev: summary.country === 'AU' ? '890' : null, experimental: summary.id === 'lab', updated_rev: 7 }
}

function ap(partial: Partial<APView> & Pick<APView, 'id' | 'name' | 'site_id'>): APView {
  return {
    model: 'AP33-WW',
    model_source: 'ap',
    firmware: 'apfw-0.15.35064-noro-84df',
    firmware_tested: true,
    status: 'in_sync',
    flags: [],
    connected: true,
    connected_at: ago(3 * 86400),
    last_seen: ago(4),
    lan_ip: '192.0.2.21',
    remote_address: '198.51.100.7',
    behind_nat: true,
    pinned_ip: '',
    has_secret: true,
    config_revision: 42,
    applied_revision: 42,
    last_error: '',
    client_count: 0,
    uptime_seconds: 3 * 86400 + 4 * 3600,
    radios: [],
    updated_rev: 3,
    ...partial,
  }
}

export const aps: APView[] = [
  ap({
    id: '02-aa-00-00-00-01',
    name: 'Hallway',
    site_id: 'home',
    client_count: 15,
    radios: [
      { band: '2.4', channel: 1, width: 20, clients: 4, util: 31 },
      { band: '5', channel: 149, width: 80, clients: 11, util: 18 },
    ],
  }),
  ap({
    id: '02-aa-00-00-00-02',
    name: 'Garage',
    site_id: 'home',
    status: 'offline',
    connected: false,
    last_seen: ago(12 * 60),
    lan_ip: '192.0.2.22',
    client_count: 0,
    uptime_seconds: 0,
  }),
  ap({
    id: '02-aa-00-00-00-03',
    name: 'Study',
    site_id: 'home',
    status: 'pending',
    lan_ip: '192.0.2.23',
    config_revision: 43,
    applied_revision: 42,
    client_count: 4,
    radios: [
      { band: '2.4', channel: 6, width: 20, clients: 1, util: 22 },
      { band: '5', channel: 36, width: 80, clients: 3, util: 9 },
    ],
  }),
  ap({
    id: '02-aa-00-00-00-10',
    name: 'Lab bench',
    site_id: 'lab',
    lan_ip: '192.0.2.40',
    remote_address: '192.0.2.40',
    behind_nat: false,
    client_count: 2,
    radios: [{ band: '5', channel: 44, width: 40, clients: 2, util: 5 }],
  }),
]

export function apDetail(view: APView): APDetail {
  return {
    ...view,
    identity: {
      serial: `A0000${view.id.slice(-2)}`,
      sku: 'AP33-WW',
      boot_count: 12,
      last_reboot_reason: 'power-on',
      last_reboot_time: ago(view.uptime_seconds),
      last_boot_state: 'ok',
      tunnel_ipv6: '',
    },
    connection: {
      connection_count: 6,
      last_disconnect: ago(3600),
      tls_cipher: 'TLS_AES_128_GCM_SHA256',
      rtt_ms: 18,
      pmtu: 1500,
      push_mode: 'reconnect',
      clock_skew_s: 0,
    },
    health: {
      cpu_temp_c: 61,
      cpu_busy: 12,
      mem_total_bytes: 512 * 1024 ** 2,
      mem_available_bytes: 210 * 1024 ** 2,
      load: [0.31, 0.28, 0.25],
      rw_total_bytes: 64 * 1024 ** 2,
      rw_available_bytes: 51 * 1024 ** 2,
      agent_heap_bytes: null,
    },
    locate_until: null,
  }
}

export const pending: PendingAP[] = [
  {
    id: '02-aa-00-00-00-20',
    model: 'AP33-WW',
    sku: 'AP33-WW',
    serial: 'A0000020',
    firmware: 'apfw-0.15.35064-noro-84df',
    local_ip: '192.0.2.30',
    remote_address: '198.51.100.7',
    first_seen: ago(600),
    last_seen: ago(20),
    attempts: 14,
    reason: 'unregistered',
  },
]

export const events: EventView[] = [
  {
    id: 'ev-3',
    site_id: 'home',
    ap_id: '02-aa-00-00-00-02',
    ap_name: 'Garage',
    kind: 'connection',
    pace_type: null,
    severity: 'warning',
    title: 'Garage disconnected',
    detail: 'The session closed: ping timeout.',
    client_mac: '',
    wlan_id: '',
    first_seen: ago(12 * 60),
    last_seen: ago(12 * 60),
    count: 1,
    resolved_by: null,
    data: {},
  },
  {
    id: 'ev-2',
    site_id: 'home',
    ap_id: '02-aa-00-00-00-03',
    ap_name: 'Study',
    kind: 'config',
    pace_type: null,
    severity: 'info',
    title: 'Study fetched config rev 43',
    detail: '',
    client_mac: '',
    wlan_id: '',
    first_seen: ago(90),
    last_seen: ago(90),
    count: 1,
    resolved_by: null,
    data: {},
  },
  {
    id: 'ev-1',
    site_id: 'home',
    ap_id: '02-aa-00-00-00-01',
    ap_name: 'Hallway',
    kind: 'dhcp_failure',
    pace_type: 12,
    severity: 'error',
    title: 'DHCP failures on VLAN 30',
    detail: '3 clients did not get an address.',
    client_mac: '02-aa-bb-00-00-01',
    wlan_id: 'wlan-example',
    first_seen: ago(5400),
    last_seen: ago(4800),
    count: 3,
    resolved_by: null,
    data: { vlan: 30 },
  },
]

export const clients: ClientView[] = [
  {
    mac: '02-aa-bb-00-00-01',
    private_mac: true,
    name: 'Phone',
    hostname: 'phone',
    alias: null,
    ip: '192.0.2.101',
    self_assigned: false,
    ipv6: [],
    ap_id: '02-aa-00-00-00-01',
    ap_name: 'Hallway',
    wlan_id: 'wlan-example',
    ssid: 'Example',
    band: '5',
    channel: 149,
    width: 80,
    bssid: '02-aa-00-00-01-10',
    protocol: 'ax',
    streams: 2,
    rssi: -54,
    avg_rssi: -55,
    snr: 38,
    signal: 'good',
    tx_rate_mbps: 864,
    rx_rate_mbps: 720,
    down_bytes: 1_204_000_000,
    up_bytes: 88_000_000,
    connected_seconds: 7200,
    idle_ms: 300,
  },
]

export const system: SystemView = {
  version: '0.3.0-dev',
  commit: '0000000',
  go: 'go1.27',
  started_at: ago(86400),
  ready: true,
  wss: {
    url: 'wss://ep-terminator.example.net',
    listen: '0.0.0.0:443',
    cert: { subject: 'CN=ep-terminator.example.net', sans: ['ep-terminator.example.net'], issuer: 'CN=Example CA', not_after: ago(-60 * 86400) },
    chain: [{ subject: 'CN=Example CA', not_after: ago(-400 * 86400), role: 'root' }],
    sessions: 3,
  },
  dns_mode: false,
  trusted_proxies: [],
  db: {
    path: '/state/controller.db',
    size_bytes: 3 * 1024 ** 2,
    free_bytes: 900 * 1024 ** 2,
    schema: 1,
    compat: 1,
    rollback_without_restore: true,
    retention: { events: '30d', samples: '14d' },
  },
  backup: { last_success: ago(3600), next: ago(-82800), keep: 7, max_bytes: 512 * 1024 ** 2, used_bytes: 21 * 1024 ** 2, dir: '/state/backups', pre_migration: [] },
  metrics: { listen: '127.0.0.1:9090', remote: false, names: false, clients: 'off' },
  migration: { state: 'not_run', reason: '', report_available: false },
}

export const onboarding: OnboardingView = {
  wss_url: 'wss://ep-terminator.example.net',
  dns_mode: false,
  ca_pems: [{ name: 'Example CA', pem: '-----BEGIN CERTIFICATE-----\nMIIB…synthetic…\n-----END CERTIFICATE-----' }],
  cloud_ini: '[cloud]\nhost = ep-terminator.example.net\n',
}

export const tokens: TokenView[] = [{ id: 'tok1', name: 'Laptop CLI', scope: 'admin', created: ago(7 * 86400), last_used: ago(3600) }]
