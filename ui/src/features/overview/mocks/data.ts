import type { APDetail, APView, AuthSessionView, ClientDetail, ClientView, EventView, PendingAP, RadioView, SessionView, TokenView } from '@/gen/api.gen'
import type { Neighbour, SamplePoint } from '@/api/queries/aps'

// Synthetic fixtures for workstream E's screens (design §4.9 shapes). They
// reuse the AP and site IDs of WS-D's mocks (api/mocks/data.ts) so the
// site stream and the shell agree. Documentation addresses only
// (192.0.2.0/24, 198.51.100.0/24), locally administered MACs (02-aa-…),
// SSID "Example". Nothing here is real.

const ago = (seconds: number, now = Date.now()) => new Date(now - seconds * 1000).toISOString()

export const SITE = 'home'

export function baseAPs(): APView[] {
  const ap = (partial: Partial<APView> & Pick<APView, 'id' | 'name'>): APView => ({
    site_id: SITE,
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
  })
  return [
    ap({
      id: '02-aa-00-00-00-01',
      name: 'Hallway',
      client_count: 15,
      radios: [
        { band: '2.4', channel: 1, width: 20, clients: 4, util: 0.31 },
        { band: '5', channel: 149, width: 80, clients: 11, util: 0.18 },
      ],
    }),
    ap({ id: '02-aa-00-00-00-02', name: 'Garage', status: 'offline', connected: false, connected_at: null, last_seen: ago(12 * 60), lan_ip: '192.0.2.22', uptime_seconds: 0 }),
    ap({
      id: '02-aa-00-00-00-03',
      name: 'Study',
      status: 'pending',
      lan_ip: '192.0.2.23',
      config_revision: 43,
      applied_revision: 42,
      client_count: 4,
      firmware: 'apfw-0.15.36000-noro-0000',
      firmware_tested: false,
      radios: [
        { band: '2.4', channel: 6, width: 20, clients: 1, util: 0.22 },
        { band: '5', channel: 36, width: 80, clients: 3, util: 0.09 },
      ],
    }),
  ]
}

export function detailFor(view: APView): APDetail {
  const suffix = view.id.slice(-2)
  const online = view.connected
  const access = view.radios.map((radio): RadioView => ({
    dev: radio.band === '5' ? 'r0' : 'r1',
    role: 'access' as const,
    band: radio.band,
    channel: radio.channel,
    secondary: 0,
    width: radio.width,
    phy: 9,
    tx_power_dbm: null,
    max_power_dbm: radio.band === '5' ? 17 : 12,
    noise_dbm: radio.band === '5' ? -92 : -88,
    util: radio.util,
    util_parts: { tx: radio.util * 0.35, rx_in_bss: radio.util * 0.4, rx_other_bss: radio.util * 0.15, non_wifi: radio.util * 0.05, unknown_wifi: radio.util * 0.05 },
    clients: radio.clients,
    dfs: null,
    counters: (radio.band === '2.4' ? { tx_retries: 41, tx_failed: 2, reinits: 0 } : { tx_retries: 12 }) as Record<string, number>,
  }))
  return {
    ...view,
    identity: {
      serial: `A0000${suffix}`,
      sku: 'AP33-WW',
      boot_count: 12,
      last_reboot_reason: 'power-on',
      // Bug B: the hello reports the reboot time as the hello time.
      last_reboot_time: online ? (view.connected_at ?? ago(60)) : null,
      last_boot_state: 'ok',
      tunnel_ipv6: online ? '2001:db8::21' : '',
    },
    connection: {
      connection_count: 6,
      last_disconnect: ago(3 * 86400 + 60),
      tls_cipher: 'TLS_AES_128_GCM_SHA256',
      rtt_ms: online ? 18 : null,
      pmtu: online ? 1500 : null,
      push_mode: 'reconnect',
      clock_skew_s: online ? 0.4 : null,
    },
    config: {
      effective_hash: 'e1:5f3a9c0d2b7e41a6c8d0f1e2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6',
      applied_timestamp: 1_763_100_000,
      desired_timestamp: 1_763_100_000,
      recovered_from_bad_config: false,
      good_5min: true,
      shadowed: [],
      compile_error: '',
      last_verification: online ? { at: ago(3 * 86400 - 120), ok: true, mismatches: [] } : null,
      country_reported: 'AU',
    },
    protocol_features: { subscribe_reply: false, file_discard: false, site_rest_404: false, pong2: false, config_change_notify: false },
    local_files: [],
    health: {
      cpu_temp_c: online ? 61 : null,
      cpu_busy: online ? 12 : null,
      mem_total_bytes: 512 * 1024 ** 2,
      mem_available_bytes: 210 * 1024 ** 2,
      load: [0.31, 0.28, 0.25],
      rw_total_bytes: 64 * 1024 ** 2,
      rw_available_bytes: 51 * 1024 ** 2,
      agent_heap_bytes: 21 * 1024 ** 2,
    },
    uplink: {
      ports: [
        { name: 'eth0', up: online, speed_mbps: 1000, duplex: 'full', rx_bps: 2_400_000, tx_bps: 18_100_000, errors: {} },
        { name: 'eth1', up: false, speed_mbps: 0, duplex: '', rx_bps: 0, tx_bps: 0, errors: {} },
      ],
      lldp: online
        ? [
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
          ]
        : [],
      power: online ? { source: 'LLDP', allocated_w: 25.5, requested_w: 25.5, needed_w: 19.2, budget_w: 25.5, constrained: false } : null,
      inactive_vlans: suffix === '01' ? [30] : [],
    },
    ip: {
      vlan: 'vlan1',
      dhcp_state: online ? 'bound' : '',
      address: online ? view.lan_ip : '',
      prefix: 24,
      renewed_at: online ? ago(1800) : null,
      lease_seconds: 86400,
      gateway: '192.0.2.1',
      gateway_mac: '02-aa-ff-00-00-fe',
      dns: ['192.0.2.1'],
      dhcp_server: '192.0.2.1',
      domain: 'example.net',
      ntp: [],
      dad_failed: [],
    },
    radios_detail: online
      ? [
          ...access,
          {
            dev: 'r2',
            role: 'scan',
            band: null,
            channel: 165,
            secondary: 0,
            width: 20,
            phy: 9,
            tx_power_dbm: null,
            max_power_dbm: null,
            noise_dbm: -94,
            util: 0.04,
            util_parts: { rx_other_bss: 0.03, non_wifi: 0.01 },
            clients: 0,
            dfs: null,
            counters: {},
          },
        ]
      : [],
    wlans_live: view.radios.map((radio) => ({
      wlan_id: 'wlan-example',
      ssid: 'Example',
      band: radio.band,
      bssid: `02-aa-00-00-${suffix}-${radio.band === '5' ? '11' : '01'}`,
      dev: radio.band === '5' ? 'wl0.1' : 'wl1.1',
      clients: radio.clients,
    })),
    overrides: { led_brightness: null, ssh_enabled: null, rf: { '2.4': suffix === '01' ? { disabled: null, channel: 1, width: null, power_dbm: null } : null, '5': null }, management: null, eth1: null },
    rf_plan: {
      '2.4': view.radios.length ? { mode: 'static', channel: view.radios[0].channel, width: 20, power_dbm: 12, dfs_fallback: null } : null,
      '5': view.radios[1] ? { mode: 'auto', channel: view.radios[1].channel, width: 80, power_dbm: 17, dfs_fallback: null } : null,
    },
    locate_until: null,
    capabilities: { config_change_notify: null, ap_cmd: false, pong2: false },
    commands: { reboot: false, restart_radios: false, disconnect: false, locate: false, fallback: `dismist ap33 ssh root@${view.lan_ip || '<ap-lan-ip>'} reboot` },
    reboots_detected: [],
  }
}

export function basePending(): PendingAP[] {
  return [
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
}

export function baseClients(aps: APView[]): ClientView[] {
  const hallway = aps[0]
  const study = aps[2]
  const client = (partial: Partial<ClientView> & Pick<ClientView, 'mac'>): ClientView => ({
    private_mac: true,
    name: '',
    hostname: '',
    alias: null,
    ip: '192.0.2.101',
    self_assigned: false,
    ipv6: [],
    ap_id: hallway.id,
    ap_name: hallway.name,
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
    signal: 'excellent',
    tx_rate_mbps: 864,
    rx_rate_mbps: 720,
    down_bytes: 1_204_000_000,
    up_bytes: 88_000_000,
    connected_seconds: 7200,
    idle_ms: 300,
    ...partial,
  })
  return [
    client({ mac: '02-aa-bb-00-00-01', hostname: 'phone', name: 'phone' }),
    client({ mac: '02-aa-bb-00-00-02', hostname: 'laptop', name: 'laptop', ip: '192.0.2.103', rssi: -63, avg_rssi: -64, snr: 29, signal: 'good', down_bytes: 9_000_000_000 }),
    client({
      mac: '00-aa-bb-00-00-03',
      private_mac: false,
      hostname: 'printer',
      name: 'printer',
      ap_id: study.id,
      ap_name: study.name,
      band: '2.4',
      channel: 6,
      width: 20,
      bssid: '02-aa-00-00-03-01',
      protocol: 'n',
      streams: 1,
      ip: '192.0.2.102',
      rssi: -78,
      avg_rssi: -77,
      snr: 10,
      signal: 'poor',
      tx_rate_mbps: 72,
      rx_rate_mbps: 65,
      down_bytes: 4_100_000,
      up_bytes: 1_900_000,
      connected_seconds: 5 * 86400,
    }),
    client({ mac: '02-aa-bb-00-00-04', hostname: '', name: '', ip: '169.254.20.4', self_assigned: true, rssi: -70, snr: 22, signal: 'fair', band: '2.4', channel: 1, width: 20 }),
  ]
}

export function recentSessions(): SessionView[] {
  return [
    {
      site_id: SITE,
      ap_id: '02-aa-00-00-00-01',
      ap_name: 'Hallway',
      mac: '02-aa-bb-00-00-09',
      ssid: 'Example',
      ip: '192.0.2.109',
      vlan_id: 1,
      connected_at: ago(5 * 3600),
      last_seen: ago(3 * 3600),
      disconnected_at: ago(3 * 3600),
      end_reason: 'left',
      rssi: -65,
      down_bytes: 250_000_000,
      up_bytes: 12_000_000,
    },
  ]
}

const KINDS: Pick<EventView, 'kind' | 'severity' | 'title' | 'detail' | 'pace_type'>[] = [
  { kind: 'connection', severity: 'info', title: 'Connected', detail: '', pace_type: null },
  { kind: 'config', severity: 'info', title: 'Applied config rev 42', detail: '', pace_type: null },
  { kind: 'dns', severity: 'warning', title: 'DNS queries failing', detail: 'DNS queries failing', pace_type: 3 },
  { kind: 'authorized', severity: 'info', title: 'Client connected', detail: '', pace_type: 49 },
]

export function baseEvents(aps: APView[], now = Date.now()): EventView[] {
  const rows: EventView[] = [
    {
      id: 'ev-vlan',
      site_id: SITE,
      ap_id: aps[0].id,
      ap_name: aps[0].name,
      kind: 'inactive_vlan',
      pace_type: null,
      severity: 'warning',
      title: 'VLAN 30 is not reaching Hallway’s uplink',
      detail: '2 clients are failing DHCP on Example.',
      client_mac: '',
      wlan_id: 'wlan-example',
      first_seen: ago(40 * 60, now),
      last_seen: ago(5 * 60, now),
      count: 4,
      resolved_by: null,
      data: { vlan: 30, wlan: 'Example' },
    },
    {
      id: 'ev-dhcp',
      site_id: SITE,
      ap_id: aps[0].id,
      ap_name: aps[0].name,
      kind: 'dhcp_failure',
      pace_type: 1,
      severity: 'warning',
      title: 'DHCP failure on VLAN 30',
      detail: 'No offer after 4 attempts.',
      client_mac: '02-aa-bb-00-00-04',
      wlan_id: 'wlan-example',
      first_seen: ago(30 * 60, now),
      last_seen: ago(6 * 60, now),
      count: 7,
      resolved_by: null,
      data: { vlan: 30, attempts: 4, requested_ip: '', server: '', nak: false, declined: false },
    },
    {
      id: 'ev-off',
      site_id: SITE,
      ap_id: aps[1].id,
      ap_name: aps[1].name,
      kind: 'connection',
      pace_type: null,
      severity: 'warning',
      title: 'Garage disconnected',
      detail: 'The session closed: ping timeout.',
      client_mac: '',
      wlan_id: '',
      first_seen: ago(12 * 60, now),
      last_seen: ago(12 * 60, now),
      count: 1,
      resolved_by: null,
      data: { cause: 'ping timeout' },
    },
    {
      id: 'ev-auth',
      site_id: SITE,
      ap_id: aps[0].id,
      ap_name: aps[0].name,
      kind: 'authorized',
      pace_type: 49,
      severity: 'info',
      title: 'phone connected to Example',
      detail: '',
      client_mac: '02-aa-bb-00-00-01',
      wlan_id: 'wlan-example',
      first_seen: ago(2 * 3600, now),
      last_seen: ago(2 * 3600, now),
      count: 1,
      resolved_by: null,
      data: { ssid: 'Example', band: '5', timeline: [{ step: 'Probe', ms: 0 }, { step: 'Auth (SAE)', ms: 41 }, { step: 'Assoc', ms: 58 }, { step: '4-way handshake', ms: 96 }, { step: 'DHCP', ms: 410 }] },
    },
  ]
  // Older history for paging.
  for (let i = 0; i < 70; i++) {
    const k = KINDS[i % KINDS.length]
    const ap = aps[i % aps.length]
    rows.push({
      id: `ev-old-${i}`,
      site_id: SITE,
      ap_id: ap.id,
      ap_name: ap.name,
      ...k,
      title: k.kind === 'connection' ? `${ap.name} connected` : k.title,
      client_mac: k.kind === 'authorized' ? '02-aa-bb-00-00-02' : '',
      wlan_id: '',
      first_seen: ago(3 * 3600 + i * 1800, now),
      last_seen: ago(3 * 3600 + i * 1800, now),
      count: i % 9 === 0 ? 3 : 1,
      resolved_by: k.kind === 'dns' && i % 2 === 0 ? `ev-old-${i - 1}` : null,
      data: {},
    })
  }
  return rows
}

export function clientDetailFor(mac: string, clients: ClientView[], events: EventView[]): ClientDetail | null {
  const live = clients.find((c) => c.mac === mac) ?? null
  const sessions = recentSessions().filter((s) => s.mac === mac)
  if (!live && !sessions.length) return null
  return {
    site_id: SITE,
    live,
    per_antenna_rssi: live ? [live.rssi - 1, live.rssi + 2] : [],
    username: '',
    user_agent: '',
    retries: { tx: live ? 120 : 0, rx: live ? 33 : 0 },
    qos: live ? [{ ac: 'BE', tx_bytes: live.down_bytes, rx_bytes: live.up_bytes }] : [],
    sessions: live
      ? [
          {
            site_id: SITE,
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
          ...sessions,
        ]
      : sessions,
    events: events.filter((e) => e.client_mac === mac),
    next_before: null,
    meta: null,
  }
}

/** One sample per AP per step, with a daily rhythm. */
export function samplesFor(aps: APView[], rangeMs: number, stepMs: number, now = Date.now()): SamplePoint[] {
  const out: SamplePoint[] = []
  const start = Math.floor((now - rangeMs) / stepMs) * stepMs
  for (const ap of aps) {
    if (!ap.connected) continue
    for (let t = start; t <= now; t += stepMs) {
      const hour = new Date(t).getHours() + new Date(t).getMinutes() / 60
      const base = Math.max(0, Math.round((ap.client_count || 3) * (0.55 + 0.45 * Math.sin(((hour - 8) / 24) * 2 * Math.PI))))
      out.push({
        at: t,
        ap_id: ap.id,
        connected: true,
        client_count: base,
        radios: ap.radios.map((r) => ({ band: r.band, channel: r.channel, width: r.width, util: Number((r.util * (0.6 + 0.4 * Math.sin(hour))).toFixed(3)), clients: Math.round(base / 2), max_power_dbm: r.band === '5' ? 17 : 12 })),
      })
    }
  }
  return out
}

export function neighbours(): Neighbour[] {
  return [
    { ssid: 'Neighbour-5G', bssid: '02-cc-00-00-00-01', band: '5', channel: 36, width: 80, rssi: -71, seen_by: '02-aa-00-00-00-01', station_count: 6, util: 0.22, last_seen: ago(120) },
    { ssid: '', bssid: '02-cc-00-00-00-02', band: '2.4', channel: 6, width: 20, rssi: -82, seen_by: '02-aa-00-00-00-01', station_count: null, util: null, last_seen: ago(300) },
  ]
}

export function authSessions(): AuthSessionView[] {
  return [
    { id: 'sess-1', created: ago(3600), expires: ago(-11 * 3600), last_used: ago(5), remote: '198.51.100.20', user_agent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X) AppleWebKit/605.1.15 Version/26.1 Mobile/15E148 Safari/604.1', current: true },
    { id: 'sess-2', created: ago(3 * 86400), expires: ago(-86400), last_used: ago(7200), remote: '198.51.100.21', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/141.0 Safari/537.36', current: false },
  ]
}

export function tokens(): TokenView[] {
  return [{ id: 'dsm_0123456789abcdef', name: 'Laptop CLI', scope: 'admin', created: ago(7 * 86400), last_used: ago(3600) }]
}
