import type {
  APConfigView,
  BandProfileView,
  ChangeRecord,
  ExpertPatchView,
  MigrationReport,
  Neighbour,
  Recommendation,
  RFProfileView,
  Sample,
  SiteSettingsView,
  SiteSummary,
  WLANView,
} from '../types'
import { ago, aps } from './data'

// Synthetic config state for the phase 2 to 4 mocks (design §3.8 to §3.14,
// §4.9). The Home site mirrors the shape of a migrated production site:
// one WPA2/WPA3 transition WLAN lifted from a network patch, AU/890 radios
// with 5 GHz 149/80 at 17 dBm and 2.4 GHz 1/20 at 12 dBm lifted from a
// site Radios[] patch, and an empty Monitor.CCRev residual. Names and
// addresses are synthetic (SSID "Example", RFC 5737 addresses, 02-aa MACs).

// ---- regulatory table (Appendix A, corrected by the live chanspec read) ----
// AU/890 on AP33: no 160 MHz; 80 MHz blocks 36-48, 52-64, 100-112, 132-144,
// 149-161; 116 and 165 are 20 MHz only; 120-128 excluded.

const dfs5 = (channel: number) => channel >= 52 && channel <= 144
const list = (channels: number[]) => channels.map((channel) => ({ channel, dfs: dfs5(channel) }))
export const valid5: BandProfileView['valid_channels'] = [
  { width: 20, primaries: list([36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 116, 132, 136, 140, 144, 149, 153, 157, 161, 165]) },
  { width: 40, primaries: list([36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 132, 136, 140, 144, 149, 153, 157, 161]) },
  { width: 80, primaries: list([36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 132, 136, 140, 144, 149, 153, 157, 161]) },
]
export const valid24: BandProfileView['valid_channels'] = [{ width: 20, primaries: list([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13]).map((p) => ({ ...p, dfs: false })) }]

const advanced: BandProfileView['advanced'] = { taf: 'default', twt: false, antenna: 'default', min_probe_rssi: null, mu_mimo: null, txbf: null }

export function rfProfile(siteId: string): RFProfileView {
  const home = siteId === 'home'
  const siteAPs = aps.filter((ap) => ap.site_id === siteId)
  return {
    managed: !home,
    country: 'AU',
    ccrev: '890',
    band_24: { enabled: true, width: 20, channels: [1, 6, 11], dfs: false, dfs_fallback: 0, power: home ? { mode: 'fixed', dbm: 12 } : { mode: 'auto', min: 8, max: 14 }, preamble: 'auto', advanced, valid_channels: valid24 },
    band_5: {
      enabled: true,
      width: 80,
      channels: [36, 40, 44, 48, 149, 153, 157, 161],
      dfs: false,
      dfs_fallback: 0,
      power: home ? { mode: 'fixed', dbm: 17 } : { mode: 'auto', min: 10, max: 17 },
      preamble: 'auto',
      advanced,
      valid_channels: valid5,
    },
    scanning_radio: null,
    plan: siteAPs.flatMap((ap) =>
      (['2.4', '5'] as const).map((band) => {
        const radio = ap.radios.find((r) => r.band === band)
        const channel = band === '5' ? (ap.id.endsWith('03') ? 36 : 149) : ap.id.endsWith('03') ? 6 : 1
        return {
          ap_id: ap.id,
          ap_name: ap.name,
          band,
          mode: home ? ('static' as const) : ('auto' as const),
          channel,
          width: band === '5' ? 80 : 20,
          power_dbm: band === '5' ? 17 : 12,
          dfs_fallback: null,
          source: home ? ('static' as const) : ('initial' as const),
          planned_at: home ? null : ago(86400),
          actual: radio ? { channel: radio.channel, width: radio.width, max_power_dbm: band === '5' ? 17 : 12, util: radio.util, clients: radio.clients } : null,
          status: !ap.connected ? ('offline' as const) : !radio ? ('radio_missing' as const) : radio.channel === channel ? ('match' as const) : ('mismatch' as const),
        }
      }),
    ),
    recommendations: [],
    fallback_aps: [],
    updated_rev: 4,
  }
}

const ED25519 = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'

export const siteSettings = new Map<string, Omit<SiteSettingsView, keyof SiteSummary | 'ccrev' | 'experimental' | 'updated_rev' | 'rf'> & { experimental: boolean; updated_rev: number }>([
  [
    'home',
    {
      experimental: false,
      updated_rev: 7,
      ssh: {
        enabled: true,
        keys: [{ key: `${ED25519} admin@laptop`, comment: 'admin@laptop', type: 'ssh-ed25519', fingerprint: 'SHA256:3pN6Z3t7gL0c1oQm9Zr0b5d0yYcVt3V6Vq0uCwqzF2E' }],
        readonly_keys: [],
      },
      syslog: [],
      management: { vlan_id: 1, dns: [], ntp: [] },
      // Migrated objects keep what the AP runs today (design §4.6).
      ap_defaults: { led_brightness: 255, hostname_from_name: false, keep_wlans_up_on_uplink_down: false, poe_constrained: 'disable_radios', wired_client_stats: false, ble_disabled: false, eth1: null, config_recovery: null },
      protection: { blocked_clients: [], proxy_arp: false, arp_spoof_check: false, penalty_box: null, dhcp_fingerprinting: false },
      expert: { has_patch: true, shadowed: [] },
    },
  ],
  [
    'office',
    {
      experimental: true,
      updated_rev: 3,
      ssh: { enabled: false, keys: [], readonly_keys: [] },
      syslog: [{ protocol: 'udp', host: '192.0.2.50', port: 514, facility: 1, severity: 6, tag: 'ap' }],
      management: { vlan_id: 1, dns: ['192.0.2.1'], ntp: ['pool.ntp.org'] },
      ap_defaults: { led_brightness: 128, hostname_from_name: true, keep_wlans_up_on_uplink_down: false, poe_constrained: 'keep_radios', wired_client_stats: false, ble_disabled: true, eth1: null, config_recovery: null },
      protection: { blocked_clients: [{ mac: '02-aa-bb-00-00-99', note: 'Old printer' }], proxy_arp: true, arp_spoof_check: true, penalty_box: null, dhcp_fingerprinting: false },
      expert: { has_patch: false, shadowed: [] },
    },
  ],
])

export function settingsFor(siteId: string) {
  let settings = siteSettings.get(siteId)
  if (!settings) {
    // A new site gets the new defaults (design §4.6).
    settings = {
      experimental: false,
      updated_rev: 1,
      ssh: { enabled: false, keys: [], readonly_keys: [] },
      syslog: [],
      management: { vlan_id: 1, dns: [], ntp: [] },
      ap_defaults: { led_brightness: 255, hostname_from_name: true, keep_wlans_up_on_uplink_down: false, poe_constrained: 'keep_radios', wired_client_stats: false, ble_disabled: false, eth1: null, config_recovery: null },
      protection: { blocked_clients: [], proxy_arp: false, arp_spoof_check: false, penalty_box: null, dhcp_fingerprinting: false },
      expert: { has_patch: false, shadowed: [] },
    }
    siteSettings.set(siteId, settings)
  }
  return settings
}

export const siteRF = new Map<string, RFProfileView>()
export function rfFor(siteId: string): RFProfileView {
  if (!siteRF.has(siteId)) siteRF.set(siteId, rfProfile(siteId))
  return siteRF.get(siteId)!
}

function wlan(partial: Partial<WLANView> & Pick<WLANView, 'id' | 'site_id' | 'ssid'>): WLANView {
  return {
    enabled: true,
    security: 'wpa2_wpa3_personal',
    has_psk: true,
    pmf: null,
    radius: null,
    bands: ['2.4', '5'],
    vlan_id: 1,
    hidden: false,
    isolation: 'none',
    bssid_index: 1,
    band_steering: false,
    rates: 'compatible',
    min_rssi: null,
    dtim: 2,
    idle_timeout_sec: 1800,
    max_clients: null,
    roaming: { fast: 'none', ap_key_cache: false, neighbor_report: true, bss_transition: true, ap_steering: false, bss_load: true },
    broadcast: { limit: false, allow_mdns: false, allow_ssdp: false, allow_ipv6_nd: true, arp_filter: false, mcast_to_ucast: false, wireless_bridge_dhcp: false },
    dhcp_only: false,
    dhcp_dns_only: false,
    wmm_disabled: false,
    uapsd_disabled: false,
    disable_11ax: false,
    disable_11nac: false,
    group_rekey_sec: 86400,
    eapol_v1: false,
    sae_h2e_only: false,
    transition_disable: false,
    rate_limit: null,
    qos_class: '',
    honour_blocklist: true,
    option82: null,
    mpsk: null,
    schedule: null,
    mdid: null,
    expert: { has_patch: false, shadowed: [] },
    experimental_in_use: [],
    live: { clients: 0, aps_broadcasting: 0, aps_total: 0 },
    updated_rev: 1,
    ...partial,
  }
}

/** Passphrases live only here, never in views (has_psk). */
export const psks = new Map<string, string>([
  ['wlan-example', 'correct-horse-battery'],
  ['wlan-guest', 'guest-pass-2026'],
  ['wlan-office', 'office-pass-2026'],
])

export const networks: WLANView[] = [
  // Migrated: legacy defaults kept (band steering on, idle timeout 3600 s).
  wlan({
    id: 'wlan-example',
    site_id: 'home',
    ssid: 'Example',
    band_steering: true,
    idle_timeout_sec: 3600,
    live: { clients: 15, aps_broadcasting: 2, aps_total: 3 },
    updated_rev: 5,
  }),
  wlan({ id: 'wlan-guest', site_id: 'home', ssid: 'Example-Guest', enabled: false, vlan_id: 30, isolation: 'clients_and_lan', bssid_index: 2, bands: ['5'], live: { clients: 0, aps_broadcasting: 0, aps_total: 3 } }),
  wlan({ id: 'wlan-office', site_id: 'office', ssid: 'Example-Office', security: 'wpa2_personal', pmf: 'optional', live: { clients: 2, aps_broadcasting: 1, aps_total: 1 } }),
]

// ---- expert patches (stored folded; views redacted) ----

export const SITE_PATHS = [
  'LED.Brightness',
  'LED.Locator',
  'SSH.Disabled',
  'SSH.External',
  'SSH.AuthorizedKeys',
  'Radios[].Band',
  'Radios[].Disabled',
  'Radios[].Channel',
  'Radios[].Width',
  'Radios[].Power',
  'Radios[].CCRev',
  'Radios[].CCRevSP',
  'Radios[].RRM.Channel',
  'Radios[].RRM.Width',
  'Radios[].RRM.Power',
  'Monitor.CCRev',
  'Monitor.NbrScan.Enabled',
  'NTP',
  'DNS',
  'Syslog[].Host',
  'Syslog[].Port',
  'EnableWiredClientStats',
  'PoE.ConstrainedRadiosDisabled',
]
export const WLAN_PATHS = [
  'Hidden',
  'Isolation',
  'NetIsolation',
  'RoamMode',
  'DTIM.Period',
  'MaxStaAllowed',
  'Auth.AKM.WPA2_PSK',
  'Auth.AKM.WPA3_PSK',
  'Auth.SAEHash2ElementOnly',
  'Auth.TransitionDisableIndication',
  'Auth.GroupRekeyIntervalSec',
  'Dot11w.Enabled',
  'Dot11w.Required',
  'WNM.NeighborReport',
  'WNM.BSSLoad',
  'BandConfigs[].Band',
  'BandConfigs[].Steer.DenyDot11AuthRSSIThresh',
  'WCInactivity.TimeoutSec',
  'RateLimit.ClientUp',
  'RateLimit.ClientDown',
]
export const SITE_FORBIDDEN = ['WLANs', 'Timestamp', 'ID', 'Calea', 'Ipsec', 'WLCmds', 'SiteString', 'PAPIOpaque', 'DeIdent.Key']
export const WLAN_FORBIDDEN = ['ID', 'SSID', 'Enabled', 'Auth.PSK', 'Auth.MultiPSK.PSKs', 'KeyCache.KeyEncryptKey', 'BandConfigs[].BSSIDIndex', 'WLCmds', 'ISE', 'AirWatch', 'DPI', 'COA.CloudEnabled', 'Auth.WEP', 'Auth.AKM.WPA_PSK', 'Auth.AKM.WPA_EAP', 'Auth.Pairwise.TKIP', 'Auth.Pairwise.WPA1']

export const expertPatches = new Map<string, ExpertPatchView>([
  // The migrated residual: Country now drives the radios, but Monitor.CCRev stays empty (§8.5 fixture).
  ['site:home', { scope: 'site', object_id: 'home', patch: { Monitor: { CCRev: '' } }, shadowed: [], partly_shadowed: ['country'], ignored_keys: [], updated_rev: 7 }],
])

export function expertFor(scope: ExpertPatchView['scope'], id: string): ExpertPatchView {
  const key = `${scope}:${id}`
  const stored = expertPatches.get(key) ?? { scope, object_id: id, patch: {}, shadowed: [], partly_shadowed: [], ignored_keys: [], updated_rev: 1 }
  return {
    ...stored,
    paths: scope === 'wlan' ? WLAN_PATHS : SITE_PATHS,
    forbidden: scope === 'wlan' ? WLAN_FORBIDDEN : scope === 'site' ? SITE_FORBIDDEN : [...SITE_FORBIDDEN, 'Name'],
  }
}

// ---- recommendations, neighbours, samples ----

export const recommendations = new Map<string, Recommendation[]>([
  [
    'home',
    [
      { id: 'rec-idle-timeout', title: 'Shorten the idle timeout of Example to 30 minutes', detail: 'Migrated WLANs keep 3600 s. New WLANs use 1800 s, which frees client slots sooner.', scope: 'wlan', object_id: 'wlan-example', disruption: 'wlan_restart' },
      { id: 'rec-residual', title: 'Remove the empty Monitor.CCRev from the site expert patch', detail: 'The country setting already drives the radios; the residual only keeps the scan radio’s ccrev empty, as before.', scope: 'site', object_id: 'home', disruption: 'radio_bounce' },
    ],
  ],
])

export function neighbours(siteId: string): Neighbour[] {
  if (siteId !== 'home') return []
  return [
    { ssid: 'Neighbour-5G', bssid: '02-aa-cc-00-00-01', band: '5', channel: 36, width: 80, rssi: -71, seen_by: '02-aa-00-00-00-01', station_count: 4, util: 22, last_seen: ago(300) },
    { ssid: 'Neighbour', bssid: '02-aa-cc-00-00-02', band: '2.4', channel: 6, width: 20, rssi: -64, seen_by: '02-aa-00-00-00-01', station_count: null, util: 41, last_seen: ago(300) },
    { ssid: '', bssid: '02-aa-cc-00-00-03', band: '2.4', channel: 11, width: 20, rssi: -82, seen_by: '02-aa-00-00-00-03', station_count: null, util: null, last_seen: ago(900) },
    { ssid: 'Printer-Direct', bssid: '02-aa-cc-00-00-04', band: '5', channel: 149, width: 20, rssi: -88, seen_by: '02-aa-00-00-00-03', station_count: 0, util: 2, last_seen: ago(600) },
  ]
}

export function samples(siteId: string, apId: string | null, stepMinutes = 10, points = 144): Sample[] {
  const siteAPs = aps.filter((ap) => ap.site_id === siteId && (!apId || ap.id === apId))
  const base = siteAPs.reduce((sum, ap) => sum + ap.client_count, 0) || 2
  return Array.from({ length: points }, (_, index) => {
    const minutesAgo = (points - 1 - index) * stepMinutes
    const hour = new Date(Date.now() - minutesAgo * 60_000).getHours()
    // Evenings busy, small hours quiet; deterministic.
    const shape = hour >= 18 && hour <= 23 ? 1.3 : hour < 6 ? 0.35 : 0.85
    const clients = Math.max(0, Math.round(base * shape + Math.sin(index / 5) * 2))
    const c24 = Math.round(clients * 0.25)
    return {
      at: ago(minutesAgo * 60),
      ap_id: apId ?? undefined,
      clients,
      clients_24: c24,
      clients_5: clients - c24,
      radios: [
        { band: '2.4', channel: 1, width: 20, util_all: 25 + (index % 7), noise_dbm: -92, clients: c24, max_power_dbm: 12 },
        { band: '5', channel: 149, width: 80, util_all: 12 + (index % 5), noise_dbm: -95, clients: clients - c24, max_power_dbm: 17 },
      ],
    }
  })
}

// ---- changes (config history) ----

export const changes: ChangeRecord[] = [
  {
    revision: 43,
    at: ago(90),
    actor: { kind: 'session', name: 'admin (198.51.100.20)' },
    kind: 'config',
    summary: 'WLAN Example: hidden off',
    endpoint: 'PATCH /api/sites/home/networks/wlan-example',
    affected_aps: [{ id: '02-aa-00-00-00-03', name: 'Study', state: 'pending' }],
    objects: [{ kind: 'network', id: 'wlan-example', name: 'Example' }],
    diff: [{ object: 'WLAN Example', path: 'hidden', from: true, to: false }],
    aps: [{ id: '02-aa-00-00-00-03', name: 'Study', diff: [{ path: 'WLANs[Example].Hidden', from: true, to: false }] }],
    results: { '02-aa-00-00-00-03': { state: 'pending', at: null, error: '' } },
  },
  {
    revision: 42,
    at: ago(3 * 86400),
    actor: { kind: 'system', name: 'migration v2' },
    kind: 'action',
    summary: 'Migration to schema 2 committed: effective config unchanged on 3 APs',
    endpoint: 'migration',
    affected_aps: [],
    objects: [],
    diff: [],
    aps: [],
    results: {},
  },
  {
    revision: 41,
    at: ago(5 * 86400),
    actor: { kind: 'session', name: 'admin (198.51.100.20)' },
    kind: 'config',
    summary: 'WLAN Example: passphrase changed',
    endpoint: 'PATCH /api/sites/home/networks/wlan-example',
    affected_aps: [
      { id: '02-aa-00-00-00-01', name: 'Hallway', state: 'applied' },
      { id: '02-aa-00-00-00-03', name: 'Study', state: 'applied' },
    ],
    objects: [{ kind: 'network', id: 'wlan-example', name: 'Example' }],
    diff: [{ object: 'WLAN Example', path: 'psk', from: 'hmac:0011223344556677', to: 'hmac:8899aabbccddeeff' }],
    aps: [
      { id: '02-aa-00-00-00-01', name: 'Hallway', diff: [{ path: 'WLANs[Example].Auth.PSK', from: '[changed]', to: '[changed]' }] },
      { id: '02-aa-00-00-00-03', name: 'Study', diff: [{ path: 'WLANs[Example].Auth.PSK', from: '[changed]', to: '[changed]' }] },
    ],
    results: { '02-aa-00-00-00-01': { state: 'applied', at: ago(5 * 86400 - 20), error: '' }, '02-aa-00-00-00-03': { state: 'applied', at: ago(5 * 86400 - 25), error: '' } },
  },
]

// ---- migration (System › Migration) ----

export const migration: MigrationReport = {
  state: 'committed',
  reason: '',
  at: ago(3 * 86400),
  lifted: [
    { kind: 'network', id: 'wlan-example', name: 'Example', fields: ['security', 'band_steering', 'idle_timeout_sec', 'bssid_index'], residual: null },
    { kind: 'site', id: 'home', name: 'Home', fields: ['country', 'rf.band_5', 'rf.band_24', 'rf.managed'], residual: { Monitor: { CCRev: '' } } },
  ],
  aps: aps
    .filter((ap) => ap.site_id === 'home')
    .map((ap) => ({
      ap_id: ap.id,
      ap_name: ap.name,
      equal: true,
      hash_before: 'e1:9f2c…',
      hash_after: 'e1:9f2c…',
      normaliser_version: 1,
      bssid_indexes: [{ network_id: 'wlan-example', ssid: 'Example', index: 1, source: ap.connected ? ('measured' as const) : ('predicted' as const) }],
      local_files: 'absent' as const,
      mismatches: [],
    })),
  compat: 1,
  can_finalise: false,
}

// ---- AP config views ----

export function apConfig(apId: string, view: APConfigView['view']): APConfigView {
  const ap = aps.find((candidate) => candidate.id === apId)
  const siteNetworks = networks.filter((n) => n.site_id === ap?.site_id && n.enabled)
  return {
    ap_id: apId,
    view,
    revision: ap?.config_revision ?? 0,
    effective_hash: 'e1:9f2c4d0b7a1e3f5c6d8e9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d',
    normaliser_version: 1,
    config: {
      Name: ap?.name ?? apId,
      LED: { Brightness: 255 },
      Radios: [
        { Band: '5', Channel: 149, Width: 80, Power: 17, CCRev: 'AU/890', RRM: { Channel: 149, Width: 80, Power: 17 } },
        { Band: '2.4', Channel: 1, Width: 20, Power: 12, CCRev: 'AU/890', RRM: { Channel: 1, Width: 20, Power: 12 } },
      ],
      WLANs: siteNetworks.map((n) => ({
        SSID: n.ssid,
        Enabled: true,
        Bands: n.bands,
        Auth: { Type: 'psk', PSK: '[redacted]', AKM: { WPA2_PSK: true, WPA3_PSK: n.security === 'wpa2_wpa3_personal' } },
        Dot11w: { Enabled: true, Required: false },
      })),
      Monitor: { CCRev: '' },
    },
    report: { version: 1, ignored: [], type_errors: [], local_files: 'none', zero_rule: true, notes: [] },
    site_patch: { Monitor: { CCRev: '' } },
    ap_patch: {},
    h0: { recorded_at: ago(10 * 86400), config_revision: 40, normaliser_version: 1, effective_hash: 'e1:9f2c4d0b7a1e3f5c6d8e9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d', matches: true },
  }
}
