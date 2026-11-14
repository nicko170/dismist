import type { APSettingsView } from '@/api/queries/apsettings'
import type { ChangeDetail } from '@/api/queries/changes'
import type { ActorKind, DiffEntry } from '@/gen/api.gen'
import type { WLANView } from '@/api/queries/networks'
import type { Recommendation } from '@/api/queries/recommendations'
import type { BandProfileView, NeighboursView, RFProfileView } from '@/api/queries/rf'
import type { ExpertPatchView, MigrationReport, SiteConfigView } from '@/api/queries/sites'

// Synthetic fixtures for F's phase-2..4 endpoints (design §4.9). They mirror
// the shape of the live migration result (§4.7: WPA2/WPA3 transition, RF
// managed, 5 GHz 149/80 at 17 dBm and 2.4 GHz 1/20 at 12 dBm as static AP
// overrides) with documentation addresses, 02-aa-… MACs and SSID "Example".
// Nothing here is real.

const now = Date.now()
const ago = (seconds: number) => new Date(now - seconds * 1000).toISOString()

export const SSH_KEY_1 = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ2J8hQjpW31ECw7Vue82g5WXsR7ubm/RBo7ZY+cm9wK'
export const SSH_KEY_2 = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEUEUFgIbAddz8QpHk7ULufpCRX9Obj5krYposbKm/YQ'
export const SSH_KEY_2_LINE = `${SSH_KEY_2} admin@example`

const AU5 = (width: number) => {
  const all20 = [36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 116, 132, 136, 140, 144, 149, 153, 157, 161, 165]
  const wide = all20.filter((c) => c !== 116 && c !== 165)
  const dfs = (c: number) => c >= 52 && c <= 144
  return (width === 20 ? all20 : wide).map((channel) => ({ channel, dfs: dfs(channel) }))
}

function band(partial: Partial<BandProfileView> & Pick<BandProfileView, 'width' | 'channels' | 'power'>, is24: boolean): BandProfileView {
  return {
    enabled: true,
    dfs: false,
    dfs_fallback: 0,
    preamble: 'short',
    advanced: { taf: 'default', twt: true, antenna: 'default', min_probe_rssi: null, mu_mimo: null, txbf: null },
    valid_channels: is24
      ? [{ width: 20, primaries: Array.from({ length: 13 }, (_, i) => ({ channel: i + 1, dfs: false })), blocks: [] }]
      : [20, 40, 80].map((width) => ({ width, primaries: AU5(width), blocks: [] })),
    ...partial,
  }
}

export function homeRF(): RFProfileView {
  return {
    managed: true,
    country: 'AU',
    ccrev: 'AU/890',
    band_24: band({ width: 20, channels: [1, 6, 11], power: { mode: 'fixed', dbm: 12 } }, true),
    band_5: band({ width: 80, channels: [36, 149], power: { mode: 'fixed', dbm: 17 } }, false),
    scanning_radio: null,
    plan: [
      { ap_id: '02-aa-00-00-00-01', ap_name: 'Hallway', band: '5', mode: 'static', channel: 149, width: 80, power_dbm: 17, dfs_fallback: null, source: 'static', actual: { channel: 149, width: 80, max_power_dbm: 17, util: 18, clients: 11 }, status: 'match' },
      { ap_id: '02-aa-00-00-00-01', ap_name: 'Hallway', band: '2.4', mode: 'static', channel: 1, width: 20, power_dbm: 12, dfs_fallback: null, source: 'static', actual: { channel: 1, width: 20, max_power_dbm: 12, util: 31, clients: 4 }, status: 'match' },
      { ap_id: '02-aa-00-00-00-02', ap_name: 'Garage', band: '5', mode: 'auto', channel: 36, width: 80, power_dbm: 17, dfs_fallback: null, source: 'initial assignment', actual: null, status: 'offline' },
      { ap_id: '02-aa-00-00-00-02', ap_name: 'Garage', band: '2.4', mode: 'auto', channel: 6, width: 20, power_dbm: 12, dfs_fallback: null, source: 'initial assignment', actual: null, status: 'offline' },
      { ap_id: '02-aa-00-00-00-03', ap_name: 'Study', band: '5', mode: 'auto', channel: 36, width: 80, power_dbm: 17, dfs_fallback: null, source: 'initial assignment', actual: { channel: 36, width: 80, max_power_dbm: 17, util: 9, clients: 3 }, status: 'match' },
      { ap_id: '02-aa-00-00-00-03', ap_name: 'Study', band: '2.4', mode: 'auto', channel: 11, width: 20, power_dbm: 12, dfs_fallback: null, source: 'initial assignment', actual: { channel: 6, width: 20, max_power_dbm: 12, util: 22, clients: 1 }, status: 'mismatch' },
    ],
    recommendations: [],
    updated_rev: 7,
  }
}

export function labRF(): RFProfileView {
  return {
    ...homeRF(),
    band_24: band({ width: 20, channels: [1, 6, 11], power: { mode: 'auto', min: 8, max: 15 } }, true),
    band_5: band({ width: 40, channels: [36, 44], power: { mode: 'auto', min: 12, max: 20 } }, false),
    plan: [{ ap_id: '02-aa-00-00-00-10', ap_name: 'Lab bench', band: '5', mode: 'auto', channel: 44, width: 40, power_dbm: 20, dfs_fallback: null, source: 'planner, 3 Dec', actual: { channel: 44, width: 40, max_power_dbm: 20, util: 5, clients: 2 }, status: 'match' }],
    updated_rev: 2,
  }
}

export const recommendations: Record<string, Recommendation[]> = {
  home: [
    { id: 'rec-poe', title: 'Keep radios on when PoE is short (Mist default)', detail: 'Today the APs turn their radios off when the switch cannot supply full power.', scope: 'site', object_id: 'home', disruption: 'none', area: 'site' },
    { id: 'rec-dhcp-hostname', title: 'Send the AP name as DHCP hostname so your router shows it', detail: 'Turns on “Use the AP name as hostname and DHCP hostname”.', scope: 'site', object_id: 'home', disruption: 'none', area: 'site' },
    { id: 'rec-band-steering', title: 'Band steering is on for Example; Mist’s default is off', detail: 'Information only: steering nudges dual-band clients to 5 GHz.', scope: 'wlan', object_id: 'wlan-example', disruption: 'wlan_restart', area: 'wlan', informational: true },
  ],
  lab: [],
}

export function siteConfig(id: string): SiteConfigView {
  const home = id === 'home'
  return {
    id,
    name: home ? 'Home' : 'Lab',
    country: 'AU',
    timezone: 'Australia/Sydney',
    counts: home ? { aps: 3, aps_online: 2, clients: 19, wlans: 2, pending_aps: 1 } : { aps: 1, aps_online: 1, clients: 2, wlans: 1, pending_aps: 0 },
    ccrev: 'AU/890',
    experimental: !home,
    updated_rev: home ? 7 : 2,
    ssh: {
      enabled: true,
      keys: [{ key: SSH_KEY_1, comment: '', type: 'ssh-ed25519', fingerprint: 'SHA256:ThTjqOhwc4Zhcvp8nmYa7p+uDeX9DSdebH+vfbvkGbA' }],
      readonly_keys: [],
    },
    syslog: [],
    management: { vlan_id: 1, dns: [], ntp: [] },
    ap_defaults: {
      led_brightness: 255,
      hostname_from_name: false,
      keep_wlans_up_on_uplink_down: false,
      poe_constrained: 'disable_radios',
      wired_client_stats: false,
      eth1: null,
      config_recovery: null,
      ble_disabled: false,
    },
    protection: { blocked_clients: [], proxy_arp: true, arp_spoof_check: false, penalty_box: null, dhcp_fingerprinting: false },
    expert: { has_patch: false, field: 'expert', shadowed: [], partly_shadowed: [], overridden_on: {} },
    experimental_in_use: [],
    rf: home ? homeRF() : labRF(),
    typed: true,
  } as SiteConfigView
}

function wlan(partial: Partial<WLANView> & Pick<WLANView, 'id' | 'site_id' | 'ssid' | 'security' | 'bands'>): WLANView {
  return {
    owe_bssid_index: 0,
    live_vaps: [],
    mdid: 0,
    enabled: true,
    has_psk: true,
    pmf: null,
    radius: null,
    vlan_id: 1,
    hidden: false,
    isolation: 'none',
    bssid_index: 1,
    band_steering: true,
    rates: 'compatible',
    dtim: 2,
    idle_timeout_sec: 3600,
    max_clients: null,
    roaming: { fast: 'none', ap_key_cache: false, neighbor_report: true, bss_transition: true, ap_steering: false, bss_load: true },
    broadcast: { limit: false, allow_mdns: false, allow_ssdp: false, allow_ipv6_nd: true, arp_filter: false, mcast_to_ucast: false, wireless_bridge_dhcp: false },
    dhcp_only: false,
    dhcp_dns_only: false,
    group_rekey_sec: 86400,
    eapol_v1: false,
    sae_h2e_only: false,
    transition_disable: false,
    owe_ssid: '',
    min_rssi: {},
    wmm_disabled: false,
    uapsd_disabled: false,
    disable_11ax: false,
    disable_11nac: false,
    rate_limit: null,
    qos_class: 'none',
    honour_blocklist: true,
    option82: null,
    mpsk: null,
    schedule: null,
    expert: { has_patch: false, field: 'expert', shadowed: [], partly_shadowed: [], overridden_on: {} },
    experimental_in_use: [],
    live: { clients: 0, aps_broadcasting: 2, aps_total: 3 },
    updated_rev: 1,
    ...partial,
  }
}

export function networks(): WLANView[] {
  return [
    wlan({
      id: 'wlan-example',
      site_id: 'home',
      ssid: 'Example',
      security: 'wpa2_wpa3_personal',
      bands: ['2.4', '5'],
      updated_rev: 5,
      live: { clients: 15, aps_broadcasting: 2, aps_total: 3 },
      live_vaps: [
        { ap_id: '02-aa-00-00-00-01', ap_name: 'Hallway', band: '5', bssid: '02-aa-00-00-01-11', dev: 'wl0.1', clients: 11 },
        { ap_id: '02-aa-00-00-00-01', ap_name: 'Hallway', band: '2.4', bssid: '02-aa-00-00-01-21', dev: 'wl1.1', clients: 4 },
      ],
    }),
    wlan({
      id: 'wlan-iot',
      site_id: 'home',
      ssid: 'Example-IoT',
      security: 'wpa2_personal',
      pmf: 'optional',
      bands: ['2.4'],
      vlan_id: 30,
      bssid_index: 2,
      band_steering: false,
      idle_timeout_sec: 1800,
      updated_rev: 2,
      live: { clients: 4, aps_broadcasting: 2, aps_total: 3 },
    }),
    wlan({
      id: 'wlan-lab',
      site_id: 'lab',
      ssid: 'Example-LAB',
      security: 'wpa2_wpa3_personal',
      bands: ['2.4', '5'],
      band_steering: false,
      idle_timeout_sec: 1800,
      live: { clients: 2, aps_broadcasting: 1, aps_total: 1 },
    }),
  ]
}

export const MOCK_PSK: Record<string, string> = { 'wlan-example': 'example-passphrase-1', 'wlan-iot': 'example-iot-pass', 'wlan-lab': 'example-lab-pass' }

export function emptyExpert(scope: ExpertPatchView['scope'], objectId: string, patch: Record<string, unknown> = {}): ExpertPatchView {
  return { scope, object_id: objectId, patch, field: 'expert', shadowed: [], partly_shadowed: [], ignored_keys: [], updated_rev: 1 }
}

/** Per-AP phase-2 fields layered over the phase-1 AP detail. */
export function apExtras(id: string): Partial<APSettingsView> {
  const hallway = id === '02-aa-00-00-00-01'
  return {
    overrides: {
      led_brightness: null,
      ssh_enabled: null,
      rf: hallway
        ? { '5': { disabled: null, channel: 149, width: null, power_dbm: null }, '2.4': { disabled: null, channel: 1, width: null, power_dbm: null } }
        : { '2.4': null, '5': null },
      management: null,
      eth1: null,
      wired_client_stats: null,
    },
    rf_plan: hallway
      ? { '2.4': null, '5': null }
      : { '5': { mode: 'auto', channel: 36, width: 80, power_dbm: 17, dfs_fallback: null, source: 'initial assignment' }, '2.4': { mode: 'auto', channel: id.endsWith('03') ? 11 : 6, width: 20, power_dbm: 12, dfs_fallback: null, source: 'initial assignment' } },
    protocol_features: {},
    local_files: hallway ? [] : null,
    capabilities: { config_change_notify: null, ap_cmd: false, pong2: false },
    radios_detail: hallway
      ? [
          { dev: 'r0', role: 'access', band: '5', channel: 149, secondary: 0, width: 80, phy: 9, tx_power_dbm: null, max_power_dbm: 17, noise_dbm: -92, util: 18, util_parts: {}, clients: 11, dfs: null, counters: {} },
          { dev: 'r1', role: 'access', band: '2.4', channel: 1, secondary: 0, width: 20, phy: 9, tx_power_dbm: null, max_power_dbm: 12, noise_dbm: -88, util: 31, util_parts: {}, clients: 4, dfs: null, counters: {} },
          { dev: 'r2', role: 'scan', band: null, channel: 165, secondary: 0, width: 20, phy: 9, tx_power_dbm: null, max_power_dbm: null, noise_dbm: null, util: 0, util_parts: {}, clients: 0, dfs: null, counters: {} },
        ]
      : [],
    wlans_live: [],
    config: {
      effective_hash: 'e1:9c1f0d8a2b7e4c11a0f5e6d7c8b9a0f1e2d3c4b5a69788',
      applied_timestamp: 1763100000,
      desired_timestamp: 1763100000,
      recovered_from_bad_config: false,
      good_5min: true,
      shadowed: [],
      compile_error: '',
      last_verification: hallway ? { at: ago(3000), ok: true, mismatches: [] } : null,
      country_reported: 'AU',
    },
    ip: { vlan: '1', dhcp_state: 'bound', address: hallway ? '192.0.2.21' : '192.0.2.22', prefix: 24, renewed_at: ago(4000), lease_seconds: 86400, gateway: '192.0.2.1', gateway_mac: '02-aa-ff-00-00-01', dns: ['192.0.2.1'], dhcp_server: '192.0.2.1', domain: 'example.net', ntp: [], dad_failed: [] },
  }
}

export function effectiveConfig(apId: string, name: string): Record<string, unknown> {
  return {
    ID: apId,
    Name: name,
    Hostname: name,
    Country: 'AU',
    LED: { Brightness: 255 },
    Monitor: { CCRev: 'AU/890', Enabled: true },
    Radios: [
      { Band: '2.4', CCRev: 'AU/890', CCRevSP: 'AU/890', Channel: 1, Width: 20, Power: 12, RRM: { Channel: 1, Width: 20, Power: 12 } },
      { Band: '5', CCRev: 'AU/890', CCRevSP: 'AU/890', Channel: 149, Width: 80, Power: 17, RRM: { Channel: 149, Width: 80, Power: 17 } },
    ],
    SSH: { Disabled: false, External: true, AuthorizedKeys: ['ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ2J8hQjpW31ECw7Vue82g5WXsR7ubm/RBo7ZY+cm9wK'] },
    WLANs: [
      {
        ID: 'wlan-example',
        SSID: 'Example',
        Enabled: true,
        Bands: ['2.4', '5'],
        Auth: { Type: 'psk', PSK: '[redacted]', AKM: { WPA2_PSK: true, WPA3_PSK: true } },
        Dot11w: { Enabled: true, Required: false },
        Vlans: ['1'],
      },
    ],
    Timestamp: 1763100000,
  }
}

/** GET /api/system/migration; the report is internal/migrate's V2Report, redacted. */
export function migrationReport(state: MigrationReport['state']): MigrationReport {
  const legacy = state === 'legacy'
  return {
    state,
    reason: legacy ? 'A disabled WLAN comes before an enabled one, so the BSSID index cannot be proven (bssid_unresolved).' : '',
    report_available: state !== 'not_run',
    at: state === 'not_run' ? null : ago(86400 * 2),
    paths: legacy ? ['WLANs[wlan-iot].BandConfigs[2.4].BSSIDIndex'] : [],
    schema: legacy ? 1 : 2,
    compat: 1,
    copy: 'controller.db.pre-v2-20261112T000000Z',
    can_finalise: false,
    finalise_after: state === 'committed' ? ago(-28 * 86400) : null,
    finalised: false,
    report:
      state === 'not_run'
        ? null
        : {
            at: ago(86400 * 2),
            verified: !legacy,
            reason: legacy ? 'bssid_unresolved' : undefined,
            sites: [{ id: 'home', probe: '02-aa-00-00-00-01', rf_managed: true, typed: { rf: { managed: true, band_5: { width: 80, power: { mode: 'fixed', dbm: 17 } }, band_24: { width: 20, power: { mode: 'fixed', dbm: 12 } } } } }],
            networks: [
              { id: 'wlan-example', site_id: 'home', ssid: 'Example', security: 'wpa2_wpa3_personal', bssid_index: 1, bssid_source: 'measured', typed: { security: 'wpa2_wpa3_personal', band_steering: true, idle_timeout_sec: 3600 } },
              { id: 'wlan-iot', site_id: 'home', ssid: 'Example-IoT', security: 'wpa2_personal', bssid_index: 2, bssid_source: legacy ? 'predicted' : 'measured', typed: {} },
            ],
            aps: [{ id: '02-aa-00-00-00-01', site_id: 'home', compiled: true, local_files: 'none', rf_override: { '5': { channel: 149 }, '2.4': { channel: 1 } }, equal: !legacy }],
          },
  }
}

/** The mock's working shape of a change: per-object and per-AP diffs, folded into ChangeDetail by toDetail. */
export type MockChange = Omit<ChangeDetail, 'objects' | 'diff' | 'snapshot' | 'effective' | 'results' | 'actor' | 'affected_aps'> & {
  actor: { kind: ActorKind; name: string }
  affected_aps: { id: string; name: string; state: string }[]
  objects: { kind: 'site' | 'network' | 'ap' | 'rf'; id: string; name: string; diff: DiffEntry[] }[]
  aps: { id: string; name: string; diff: DiffEntry[] }[]
}

/** GET /api/sites/$id/changes/$rev (internal/api ChangeDetail). */
export function toDetail(change: MockChange): ChangeDetail {
  const { objects, aps, actor, affected_aps, ...rest } = change
  return {
    ...rest,
    actor: { ...actor, id: actor.kind === 'session' ? 'a1b2c3d4' : '', remote: actor.kind === 'session' ? '198.51.100.20' : '' },
    affected_aps: affected_aps.map((ap) => ({ ...ap, error: '' })),
    objects: objects.map(({ kind, id, name }) => ({ kind: kind === 'rf' ? 'site' : kind, id, name })),
    diff: objects.flatMap((object) => object.diff),
    snapshot: null,
    effective: Object.fromEntries(aps.map((ap) => [ap.id, `e2:${ap.id.slice(-2)}`])),
    results: Object.fromEntries(affected_aps.map((ap) => [ap.id, { state: ap.state, at: null, error: '' }])),
  }
}

export function changes(): ChangeDetail[] {
  return mockChanges().map(toDetail)
}

function mockChanges(): MockChange[] {
  const actor = { kind: 'session' as const, name: 'admin (browser)' }
  const hallway = { id: '02-aa-00-00-00-01', name: 'Hallway' }
  return [
    {
      revision: 47,
      at: ago(1800),
      actor,
      summary: 'WLAN Example-IoT: VLAN 30',
      kind: 'config',
      affected_aps: [{ ...hallway, state: 'applied' }, { id: '02-aa-00-00-00-03', name: 'Study', state: 'pending' }],
      endpoint: 'PATCH /api/sites/home/networks/wlan-iot',
      objects: [{ kind: 'network', id: 'wlan-iot', name: 'Example-IoT', diff: [{ path: 'vlan_id', from: 1, to: 30 }] }],
      aps: [{ ...hallway, diff: [{ path: 'WLANs[Example-IoT].Vlans', from: ['1'], to: ['30'] }, { path: 'SwitchPorts[eth0].Vlans', from: ['1'], to: ['1', '30'] }] }],
      restorable: true,
    },
    {
      revision: 46,
      at: ago(7200),
      actor,
      summary: 'WLAN Example-IoT: passphrase changed',
      kind: 'config',
      affected_aps: [{ ...hallway, state: 'applied' }],
      endpoint: 'PATCH /api/sites/home/networks/wlan-iot',
      objects: [{ kind: 'network', id: 'wlan-iot', name: 'Example-IoT', diff: [{ path: 'psk', from: '[changed]', to: '[changed]' }] }],
      aps: [{ ...hallway, diff: [{ path: 'WLANs[Example-IoT].Auth.PSK', from: '[changed]', to: '[changed]' }] }],
      restorable: true,
    },
    {
      revision: 45,
      at: ago(86400),
      actor,
      summary: 'Created WLAN Example-IoT',
      kind: 'config',
      affected_aps: [{ ...hallway, state: 'applied' }],
      endpoint: 'POST /api/sites/home/networks',
      objects: [{ kind: 'network', id: 'wlan-iot', name: 'Example-IoT', diff: [{ path: '(created)', from: null, to: 'Example-IoT' }] }],
      aps: [{ ...hallway, diff: [{ path: 'WLANs[Example-IoT]', from: null, to: '(added)' }] }],
      restorable: true,
    },
    {
      revision: 44,
      at: ago(86400 * 2),
      actor: { kind: 'system', name: 'dismist' },
      summary: 'Migrated to schema 2',
      kind: 'config',
      affected_aps: [],
      endpoint: 'startup',
      objects: [],
      aps: [],
      restorable: true,
    },
    {
      revision: 43,
      at: ago(86400 * 3),
      actor: { kind: 'token', name: 'Laptop CLI' },
      summary: 'Located AP Hallway for 5 min',
      kind: 'action',
      affected_aps: [],
      endpoint: 'POST /api/aps/02-aa-00-00-00-01/actions/locate',
      objects: [],
      aps: [],
      restorable: false,
    },
  ]
}

export function neighbours(): NeighboursView {
  return {
    scans: [
      { ap_id: '02-aa-00-00-00-01', ap_name: 'Hallway', band: '5', at: ago(240), entries: 4 },
      { ap_id: '02-aa-00-00-00-03', ap_name: 'Study', band: '5', at: ago(900), entries: 2 },
    ],
    neighbours: [
      { ssid: 'Neighbour-A', bssid: '02-bb-00-00-00-01', band: '5', channel: 36, width: 80, rssi: -71, seen_by: '02-aa-00-00-00-01', seen_by_name: 'Hallway', station_count: 4, util: 0.21, last_seen: ago(240) },
      { ssid: 'Neighbour-B', bssid: '02-bb-00-00-00-02', band: '5', channel: 149, width: 80, rssi: -83, seen_by: '02-aa-00-00-00-01', seen_by_name: 'Hallway', station_count: null, util: null, last_seen: ago(240) },
      { ssid: '', bssid: '02-bb-00-00-00-03', band: '5', channel: 100, width: 80, rssi: -88, seen_by: '02-aa-00-00-00-03', seen_by_name: 'Study', station_count: 1, util: 0.05, last_seen: ago(900) },
      { ssid: 'Neighbour-C', bssid: '02-bb-00-00-00-04', band: '2.4', channel: 6, width: 20, rssi: -64, seen_by: '02-aa-00-00-00-01', seen_by_name: 'Hallway', station_count: 7, util: 0.44, last_seen: ago(240) },
      { ssid: 'Neighbour-D', bssid: '02-bb-00-00-00-05', band: '2.4', channel: 11, width: 20, rssi: -79, seen_by: '02-aa-00-00-00-03', seen_by_name: 'Study', station_count: 2, util: 0.12, last_seen: ago(900) },
    ],
    own: [{ ssid: 'Example', bssid: '02-aa-00-00-01-11', band: '5', channel: 149, width: 80, rssi: -40, seen_by: '02-aa-00-00-00-03', seen_by_name: 'Study', station_count: 11, util: 0.18, last_seen: ago(900) }],
  }
}
