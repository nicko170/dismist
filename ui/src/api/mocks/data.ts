import type {
  APDetail,
  APView,
  AuthSessionView,
  ClientDetail,
  ClientMeta,
  ClientView,
  EventView,
  OnboardingView,
  PendingAP,
  SessionView,
  SiteSummary,
  SiteView,
  SystemView,
  TokenView,
} from '../types'

// Synthetic fixtures for the phase-1 shapes of design §4.9, typed with the
// generated contract (src/gen/api.gen.ts), so drift fails `tsc`. Documentation
// addresses only (RFC 5737 192.0.2.0/24 and 198.51.100.0/24), locally
// administered MACs (02-aa-…), SSID "Example". Nothing here is real.

export const MOCK_ADMIN_TOKEN = 'a1'.repeat(32)
export const MOCK_API_TOKEN = 'dsm_mock_0123456789abcdef'

const now = Date.now()
export const ago = (seconds: number) => new Date(now - seconds * 1000).toISOString()

export const sites: SiteSummary[] = [
  {
    id: 'home',
    name: 'Home',
    country: 'AU',
    timezone: 'Australia/Sydney',
    counts: { aps: 3, aps_online: 2, clients: 19, wlans: 2, pending_aps: 1 },
  },
  {
    id: 'office',
    name: 'Office',
    country: 'AU',
    timezone: 'Australia/Sydney',
    counts: { aps: 1, aps_online: 1, clients: 2, wlans: 1, pending_aps: 0 },
  },
]

/** The phase-1 part of a SiteView; workstream F's config handlers (registered first) serve the typed settings. */
export function siteView(summary: SiteSummary): SiteView {
  return { ...summary, ccrev: summary.country === 'AU' ? '890' : null, experimental: false, updated_rev: 7 } as SiteView
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
      { band: '2.4', channel: 1, width: 20, clients: 4, util: 0.31 },
      { band: '5', channel: 149, width: 80, clients: 11, util: 0.18 },
    ],
  }),
  ap({
    id: '02-aa-00-00-00-02',
    name: 'Garage',
    site_id: 'home',
    status: 'offline',
    connected: false,
    connected_at: null,
    last_seen: ago(12 * 60),
    lan_ip: '192.0.2.22',
    client_count: 0,
    uptime_seconds: 0,
  }),
  ap({
    id: '02-aa-00-00-00-03',
    name: 'Study',
    site_id: 'home',
    status: 'applying',
    lan_ip: '192.0.2.23',
    config_revision: 43,
    applied_revision: 42,
    client_count: 4,
    radios: [
      { band: '2.4', channel: 6, width: 20, clients: 1, util: 0.22 },
      { band: '5', channel: 36, width: 80, clients: 3, util: 0.09 },
    ],
  }),
  ap({
    id: '02-aa-00-00-00-10',
    name: 'Front desk',
    site_id: 'office',
    lan_ip: '192.0.2.40',
    remote_address: '192.0.2.40',
    behind_nat: false,
    client_count: 2,
    radios: [{ band: '5', channel: 44, width: 40, clients: 2, util: 0.05 }],
  }),
]

export function apDetail(view: APView): APDetail {
  const suffix = view.id.slice(-2)
  const online = view.connected
  return {
    ...view,
    identity: {
      serial: `A0000${suffix}`,
      sku: 'AP33-WW',
      boot_count: 12,
      last_reboot_reason: 'power-on',
      last_reboot_time: online ? ago(view.uptime_seconds) : null,
      last_boot_state: 'ok',
      tunnel_ipv6: '',
    },
    connection: {
      connection_count: 6,
      last_disconnect: ago(3600),
      tls_cipher: 'TLS_AES_128_GCM_SHA256',
      rtt_ms: online ? 18 : null,
      pmtu: online ? 1500 : null,
      push_mode: 'reconnect',
      clock_skew_s: online ? 0 : null,
    },
    config: {
      effective_hash: 'hmac-sha256:v1:0000000000000000000000000000000000000000000000000000000000000000',
      applied_timestamp: 1_763_100_000,
      desired_timestamp: 1_763_100_000,
      recovered_from_bad_config: false,
      good_5min: true,
      shadowed: [],
      compile_error: '',
      last_verification: null,
      country_reported: 'AU',
    },
    protocol_features: { subscribe_reply: false, file_discard: false, site_rest_404: false, pong2: false },
    local_files: [],
    health: {
      cpu_temp_c: online ? 61 : null,
      cpu_busy: online ? 12 : null,
      mem_total_bytes: 512 * 1024 ** 2,
      mem_available_bytes: 210 * 1024 ** 2,
      load: [0.31, 0.28, 0.25],
      rw_total_bytes: 64 * 1024 ** 2,
      rw_available_bytes: 51 * 1024 ** 2,
      agent_heap_bytes: null,
    },
    uplink: {
      ports: [
        { name: 'eth0', up: online, speed_mbps: 1000, duplex: 'full', rx_bps: 2_400_000, tx_bps: 18_100_000, errors: {} },
        { name: 'eth1', up: false, speed_mbps: 0, duplex: '', rx_bps: 0, tx_bps: 0, errors: {} },
      ],
      lldp: [
        {
          port: 'eth0',
          system_name: 'switch-example',
          system_desc: 'Example switch',
          port_id: 'ge-0/0/7',
          port_desc: 'AP uplink',
          chassis_id: '02-aa-ff-00-00-01',
          mgmt_address: '192.0.2.2',
          native_vlan: 1,
          vlans: [1, 30],
        },
      ],
      power: { source: 'PoE 802.3at', allocated_w: 25.5, requested_w: 25.5, needed_w: 19.2, budget_w: 25.5, constrained: false },
      inactive_vlans: [],
    },
    ip: {
      vlan: 'vlan1',
      dhcp_state: 'bound',
      address: view.lan_ip,
      prefix: 24,
      renewed_at: ago(1800),
      lease_seconds: 86400,
      gateway: '192.0.2.1',
      gateway_mac: '02-aa-ff-00-00-fe',
      dns: ['192.0.2.1'],
      dhcp_server: '192.0.2.1',
      domain: 'example.net',
      ntp: [],
      dad_failed: [],
    },
    radios_detail: [
      ...view.radios.map((radio, index) => ({
        dev: `r${index}`,
        role: 'access' as const,
        band: radio.band,
        channel: radio.channel,
        secondary: 0,
        width: radio.width,
        phy: radio.band === '5' ? 6 : 5,
        tx_power_dbm: radio.band === '5' ? 17 : 12,
        max_power_dbm: radio.band === '5' ? 17 : 12,
        noise_dbm: -92,
        util: radio.util,
        util_parts: { tx: radio.util * 0.4, rx_in_bss: radio.util * 0.35, rx_other_bss: radio.util * 0.15, non_wifi: radio.util * 0.1 },
        clients: radio.clients,
        dfs: null,
        counters: {},
      })),
      {
        dev: 'r2',
        role: 'scan',
        band: null,
        channel: 0,
        secondary: 0,
        width: 0,
        phy: 0,
        tx_power_dbm: null,
        max_power_dbm: null,
        noise_dbm: null,
        util: 0,
        util_parts: {},
        clients: 0,
        dfs: null,
        counters: {},
      },
    ],
    wlans_live: view.radios.map((radio, index) => ({
      wlan_id: 'wlan-example',
      ssid: 'Example',
      band: radio.band,
      bssid: `02-aa-00-00-${suffix}-${index}1`,
      dev: `wl${index}.1`,
      clients: radio.clients,
    })),
    overrides: { led_brightness: null, ssh_enabled: null, rf: { '2.4': null, '5': null }, management: null, eth1: null },
    rf_plan: { '2.4': null, '5': null },
    locate_until: null,
    capabilities: { config_change_notify: null, ap_cmd: false, pong2: false },
    commands: { reboot: false, restart_radios: false, disconnect: false, locate: false, fallback: `dismist ap33 ssh root@${view.lan_ip || '<ap-lan-ip>'} reboot` },
    reboots_detected: [],
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
    dismissed: false,
    dismissed_until: null,
  },
]

function event(partial: Partial<EventView> & Pick<EventView, 'id' | 'ap_id' | 'title'>): EventView {
  return {
    site_id: 'home',
    ap_name: aps.find((a) => a.id === partial.ap_id)?.name ?? '',
    kind: 'connection',
    pace_type: null,
    severity: 'info',
    detail: '',
    client_mac: '',
    wlan_id: '',
    first_seen: ago(60),
    last_seen: ago(60),
    count: 1,
    resolved_by: null,
    data: {},
    ...partial,
  }
}

export const events: EventView[] = [
  event({
    id: 'ev-3',
    ap_id: '02-aa-00-00-00-02',
    severity: 'warning',
    title: 'Garage disconnected',
    detail: 'The session closed: ping timeout.',
    first_seen: ago(12 * 60),
    last_seen: ago(12 * 60),
  }),
  event({ id: 'ev-2', ap_id: '02-aa-00-00-00-03', kind: 'config', title: 'Study fetched config rev 43', first_seen: ago(90), last_seen: ago(90) }),
  event({
    id: 'ev-1',
    ap_id: '02-aa-00-00-00-01',
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
    data: { vlan: 30 },
  }),
  event({ id: 'ev-0', ap_id: '02-aa-00-00-00-10', site_id: 'office', title: 'Front desk connected', first_seen: ago(7200), last_seen: ago(7200) }),
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
    bssid: '02-aa-00-00-01-11',
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
  {
    mac: '02-aa-bb-00-00-02',
    private_mac: false,
    name: 'Printer',
    hostname: 'printer',
    alias: null,
    ip: '192.0.2.102',
    self_assigned: false,
    ipv6: [],
    ap_id: '02-aa-00-00-00-03',
    ap_name: 'Study',
    wlan_id: 'wlan-example',
    ssid: 'Example',
    band: '2.4',
    channel: 6,
    width: 20,
    bssid: '02-aa-00-00-03-01',
    protocol: 'n',
    streams: 1,
    rssi: -71,
    avg_rssi: -70,
    snr: 21,
    signal: 'fair',
    tx_rate_mbps: 72,
    rx_rate_mbps: 65,
    down_bytes: 4_100_000,
    up_bytes: 1_900_000,
    connected_seconds: 5 * 86400,
    idle_ms: 4000,
  },
]

export function clientSessions(mac: string): SessionView[] {
  const live = clients.find((c) => c.mac === mac)
  if (!live) return []
  return [
    {
      site_id: 'home',
      ap_id: live.ap_id,
      ap_name: live.ap_name,
      mac,
      ssid: live.ssid,
      ip: live.ip,
      vlan_id: 1,
      connected_at: ago(live.connected_seconds),
      last_seen: ago(2),
      disconnected_at: null,
      end_reason: '',
      rssi: live.rssi,
      down_bytes: live.down_bytes,
      up_bytes: live.up_bytes,
    },
  ]
}

/** PUT /api/clients/$mac/meta store (the client_meta bucket). */
export const clientMetas = new Map<string, ClientMeta>()

export function clientDetail(mac: string): ClientDetail | null {
  const live = clients.find((c) => c.mac === mac) ?? null
  if (!live) return null
  return {
    site_id: 'home',
    live,
    per_antenna_rssi: [live.rssi - 1, live.rssi + 1],
    username: '',
    user_agent: '',
    retries: { tx: 12, rx: 3 },
    qos: [{ ac: 'be', tx_bytes: live.down_bytes, rx_bytes: live.up_bytes }],
    sessions: clientSessions(mac),
    events: events.filter((e) => e.client_mac === mac),
    next_before: null,
    meta: clientMetas.get(mac) ?? null,
  }
}

export const system: SystemView = {
  version: '0.4.0-dev',
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
    compat_raised_at_revision: null,
    retention: { events: '30d', samples: '14d' },
    records: { sites: 2, aps: 4, networks: 5, events_v2: 1840, samples: 40320, samples_hourly: 2688, client_sessions: 512, config_history: 46 },
  },
  backup: {
    last_success: ago(3600),
    next: ago(-82800),
    interval: '24h0m0s',
    keep: 7,
    max_bytes: 512 * 1024 ** 2,
    used_bytes: 21 * 1024 ** 2,
    count: 3,
    dir: '/state/backups',
    last_error: '',
    pre_migration: [],
  },
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

export const authSessions: AuthSessionView[] = [
  {
    id: 'sess-current',
    created: ago(3600),
    expires: ago(-11 * 3600),
    last_used: ago(5),
    remote: '198.51.100.20',
    user_agent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X)',
    current: true,
  },
  {
    id: 'sess-laptop',
    created: ago(2 * 86400),
    expires: ago(-86400),
    last_used: ago(7200),
    remote: '198.51.100.21',
    user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 15_6)',
    current: false,
  },
]
