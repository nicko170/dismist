import type { Band } from '@/gen/api.gen'
import type {
  FastRoaming,
  Isolation,
  PMF,
  QoSClass,
  RatesProfile,
  ScheduleView,
  Security,
  WLANInput,
  WLANView,
} from '@/api/queries/networks'

// WLAN form values and their mapping to the API (design §3.9, §4.9). The form
// keeps composite parts (RADIUS rows, keys, schedule text) in editing-friendly
// shapes; toInput turns them back into the WLANInput merge patch.

export type ServerRow = { host: string; port: number | string | null; secret: string; has_secret: boolean }
export type CoARow = { host: string; local_port: number | string | null; secret: string; has_secret: boolean }
export type KeyRow = {
  id: string
  name: string
  passphrase: string
  has_passphrase: boolean
  vlan_id: number | string | null
  mac: string
  expires: string
}

export const DAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const
export type Day = (typeof DAYS)[number]

export type WlanForm = {
  ssid: string
  enabled: boolean
  security: Security
  psk: string
  bands: Band[]
  vlan: { tagged: boolean; id: number | string | null }
  hidden: boolean
  isolation: Isolation
  pmf: PMF
  sae_h2e_only: boolean
  transition_disable: boolean
  group_rekey_sec: number | string | null
  eapol_v1: boolean
  owe_ssid: string
  roaming: { fast: FastRoaming; ap_key_cache: boolean; neighbor_report: boolean; bss_transition: boolean; ap_steering: boolean; bss_load: boolean }
  band_steering: boolean
  rates: RatesProfile
  /** b24/b5, not "2.4": a dot in a key is a nested path to react-hook-form. */
  min_rssi: { b24: number | string | null; b5: number | string | null }
  dtim: number | string | null
  wmm_disabled: boolean
  uapsd_disabled: boolean
  disable_11ax: boolean
  disable_11nac: boolean
  broadcast: { limit: boolean; allow_mdns: boolean; allow_ssdp: boolean; allow_ipv6_nd: boolean; arp_filter: boolean; mcast_to_ucast: boolean; wireless_bridge_dhcp: boolean }
  dhcp_only: boolean
  dhcp_dns_only: boolean
  option82: { enabled: boolean; circuit_id: string }
  max_clients: number | string | null
  idle_timeout_sec: number | string | null
  rate_limit: { wlan_up: number | string | null; wlan_down: number | string | null; client_up: number | string | null; client_down: number | string | null }
  qos_class: QoSClass
  honour_blocklist: boolean
  radius: {
    auth: ServerRow[]
    acct: ServerRow[]
    mac_auth: boolean
    acct_interim_sec: number | string | null
    nas_id: string
    nas_ip: string
    dynamic_vlan: { enabled: boolean; type: 'standard' | 'airespace-interface-name'; vlan_ids: string }
    reauth_sec: number | string | null
    reauth_on_session_timeout: boolean
    coa: { enabled: boolean; servers: CoARow[]; event_timestamp_optional: boolean }
  }
  mpsk: { enabled: boolean; private: boolean; keys: KeyRow[] }
  schedule: { enabled: boolean; days: Record<Day, string> }
}

/** New-object defaults (§4.6: research defaults, not the legacy ones). */
export function newWlanForm(): WlanForm {
  return {
    ssid: '',
    enabled: true,
    security: 'wpa2_wpa3_personal',
    psk: '',
    bands: ['2.4', '5'],
    vlan: { tagged: false, id: null },
    hidden: false,
    isolation: 'none',
    pmf: 'optional',
    sae_h2e_only: false,
    transition_disable: false,
    group_rekey_sec: 86400,
    eapol_v1: false,
    owe_ssid: '',
    roaming: { fast: 'none', ap_key_cache: false, neighbor_report: true, bss_transition: true, ap_steering: false, bss_load: true },
    band_steering: false,
    rates: 'compatible',
    min_rssi: { b24: null, b5: null },
    dtim: 2,
    wmm_disabled: false,
    uapsd_disabled: false,
    disable_11ax: false,
    disable_11nac: false,
    broadcast: { limit: false, allow_mdns: false, allow_ssdp: false, allow_ipv6_nd: true, arp_filter: false, mcast_to_ucast: false, wireless_bridge_dhcp: false },
    dhcp_only: false,
    dhcp_dns_only: false,
    option82: { enabled: false, circuit_id: '' },
    max_clients: null,
    idle_timeout_sec: 1800,
    rate_limit: { wlan_up: null, wlan_down: null, client_up: null, client_down: null },
    qos_class: 'none',
    honour_blocklist: true,
    radius: {
      auth: [],
      acct: [],
      mac_auth: false,
      acct_interim_sec: null,
      nas_id: '',
      nas_ip: '',
      dynamic_vlan: { enabled: false, type: 'standard', vlan_ids: '' },
      reauth_sec: null,
      reauth_on_session_timeout: false,
      coa: { enabled: false, servers: [], event_timestamp_optional: false },
    },
    mpsk: { enabled: false, private: false, keys: [] },
    schedule: { enabled: false, days: { mon: '', tue: '', wed: '', thu: '', fri: '', sat: '', sun: '' } },
  }
}

export type Preset = 'home' | 'guest' | 'iot'

/** §3.8 "New WLAN" presets. Guest adds a rate limit and schedule only on experimental sites. */
export function presetForm(preset: Preset | undefined, experimental: boolean): WlanForm {
  const form = newWlanForm()
  if (preset === 'guest') {
    form.isolation = 'clients_and_lan'
    if (experimental) form.rate_limit = { wlan_up: null, wlan_down: null, client_up: 5000, client_down: 20000 }
  }
  if (preset === 'iot') {
    form.security = 'wpa2_personal'
    form.bands = ['2.4']
    form.band_steering = false
  }
  return form
}

const scheduleText = (ranges: string[] | undefined) => (ranges ?? []).join(', ')

export function toForm(view: WLANView): WlanForm {
  const base = newWlanForm()
  return {
    ...base,
    ssid: view.ssid,
    enabled: view.enabled,
    security: view.security,
    psk: '',
    bands: view.bands.toSorted() as Band[],
    vlan: { tagged: view.vlan_id > 1, id: view.vlan_id > 1 ? view.vlan_id : null },
    hidden: view.hidden,
    isolation: view.isolation ?? 'none',
    pmf: view.pmf ?? 'optional',
    sae_h2e_only: view.sae_h2e_only ?? false,
    transition_disable: view.transition_disable ?? false,
    group_rekey_sec: view.group_rekey_sec ?? 86400,
    eapol_v1: view.eapol_v1 ?? false,
    owe_ssid: view.owe_ssid ?? '',
    roaming: { ...base.roaming, ...view.roaming },
    band_steering: view.band_steering,
    rates: view.rates ?? 'compatible',
    min_rssi: { b24: view.min_rssi?.['2.4'] ?? null, b5: view.min_rssi?.['5'] ?? null },
    dtim: view.dtim ?? 2,
    wmm_disabled: view.wmm_disabled ?? false,
    uapsd_disabled: view.uapsd_disabled ?? false,
    disable_11ax: view.disable_11ax ?? false,
    disable_11nac: view.disable_11nac ?? false,
    broadcast: { ...base.broadcast, ...view.broadcast },
    dhcp_only: view.dhcp_only ?? false,
    dhcp_dns_only: view.dhcp_dns_only ?? false,
    option82: { enabled: view.option82?.enabled ?? false, circuit_id: view.option82?.circuit_id ?? '' },
    max_clients: view.max_clients ?? null,
    idle_timeout_sec: view.idle_timeout_sec,
    rate_limit: { ...base.rate_limit, ...view.rate_limit },
    qos_class: view.qos_class ?? 'none',
    honour_blocklist: view.honour_blocklist ?? true,
    radius: view.radius
      ? {
          auth: view.radius.auth.map((s) => ({ host: s.host, port: s.port, secret: '', has_secret: s.has_secret })),
          acct: view.radius.acct.map((s) => ({ host: s.host, port: s.port, secret: '', has_secret: s.has_secret })),
          mac_auth: view.radius.mac_auth,
          acct_interim_sec: view.radius.acct_interim_sec || null,
          nas_id: view.radius.nas_id ?? '',
          nas_ip: view.radius.nas_ip ?? '',
          dynamic_vlan: {
            enabled: view.radius.dynamic_vlan?.enabled ?? false,
            type: view.radius.dynamic_vlan?.type ?? 'standard',
            vlan_ids: (view.radius.dynamic_vlan?.vlan_ids ?? []).join(', '),
          },
          reauth_sec: view.radius.reauth_sec || null,
          reauth_on_session_timeout: view.radius.reauth_on_session_timeout ?? false,
          coa: {
            enabled: view.radius.coa?.enabled ?? false,
            servers: (view.radius.coa?.servers ?? []).map((s) => ({ host: s.host, local_port: s.local_port, secret: '', has_secret: Boolean(s.has_secret) })),
            event_timestamp_optional: view.radius.coa?.event_timestamp_optional ?? false,
          },
        }
      : base.radius,
    mpsk: view.mpsk
      ? {
          enabled: view.mpsk.enabled,
          private: view.mpsk.private,
          keys: view.mpsk.keys.map((k) => ({
            id: k.id,
            name: k.name,
            passphrase: '',
            has_passphrase: k.has_passphrase,
            vlan_id: k.vlan_id,
            mac: k.mac,
            expires: k.expires ? k.expires.slice(0, 10) : '',
          })),
        }
      : base.mpsk,
    schedule: view.schedule
      ? {
          enabled: view.schedule.enabled,
          days: Object.fromEntries(DAYS.map((d) => [d, scheduleText(view.schedule?.days[d])])) as Record<Day, string>,
        }
      : base.schedule,
  }
}

const num = (v: number | string | null | undefined): number | null => (typeof v === 'number' ? v : v == null || v === '' ? null : Number(v))

function serverInput(rows: ServerRow[]) {
  return rows.map((row) => ({ host: row.host.trim(), port: num(row.port) ?? 0, ...(row.secret ? { secret: row.secret } : {}) }))
}

function scheduleInput(schedule: WlanForm['schedule']): ScheduleView {
  return {
    enabled: schedule.enabled,
    days: Object.fromEntries(
      DAYS.map((d) => [
        d,
        schedule.days[d]
          .split(',')
          .map((r) => r.trim())
          .filter(Boolean),
      ]),
    ) as ScheduleView['days'],
  }
}

/** The composite parts sent whole when anything inside them changed. */
const composite: Record<string, (values: WlanForm) => unknown> = {
  vlan: () => undefined,
  min_rssi: (v) => {
    const out: Partial<Record<Band, number>> = {}
    for (const band of ['2.4', '5'] as Band[]) {
      const n = num(v.min_rssi[band === '2.4' ? 'b24' : 'b5'])
      if (n != null) out[band] = n
    }
    return Object.keys(out).length ? out : null
  },
  rate_limit: (v) => {
    const r = { wlan_up: num(v.rate_limit.wlan_up), wlan_down: num(v.rate_limit.wlan_down), client_up: num(v.rate_limit.client_up), client_down: num(v.rate_limit.client_down) }
    return Object.values(r).every((x) => x == null) ? null : r
  },
  option82: (v) => (v.option82.enabled || v.option82.circuit_id ? { enabled: v.option82.enabled, circuit_id: v.option82.circuit_id } : null),
  radius: (v) => ({
    auth: serverInput(v.radius.auth),
    acct: serverInput(v.radius.acct),
    mac_auth: v.radius.mac_auth,
    acct_interim_sec: num(v.radius.acct_interim_sec) ?? 0,
    nas_id: v.radius.nas_id,
    nas_ip: v.radius.nas_ip,
    dynamic_vlan: v.radius.dynamic_vlan.enabled
      ? {
          enabled: true,
          type: v.radius.dynamic_vlan.type,
          vlan_ids: v.radius.dynamic_vlan.vlan_ids
            .split(/[\s,]+/)
            .filter(Boolean)
            .map(Number),
        }
      : null,
    reauth_sec: num(v.radius.reauth_sec) ?? 0,
    reauth_on_session_timeout: v.radius.reauth_on_session_timeout,
    coa: v.radius.coa.enabled
      ? {
          enabled: true,
          servers: v.radius.coa.servers.map((s) => ({ host: s.host.trim(), local_port: num(s.local_port) ?? 3799, ...(s.secret ? { secret: s.secret } : {}) })),
          event_timestamp_optional: v.radius.coa.event_timestamp_optional,
        }
      : null,
  }),
  mpsk: (v) =>
    v.mpsk.enabled || v.mpsk.keys.length
      ? {
          enabled: v.mpsk.enabled,
          private: v.mpsk.private,
          keys: v.mpsk.keys.map((k) => ({
            ...(k.id ? { id: k.id } : {}),
            name: k.name.trim(),
            ...(k.passphrase ? { passphrase: k.passphrase } : {}),
            vlan_id: num(k.vlan_id),
            mac: k.mac.trim(),
            expires: k.expires ? `${k.expires}T23:59:59Z` : null,
          })),
        }
      : null,
  schedule: (v) => (v.schedule.enabled || DAYS.some((d) => v.schedule.days[d]) ? scheduleInput(v.schedule) : null),
}

const numericLeaves = new Set(['group_rekey_sec', 'dtim', 'max_clients', 'idle_timeout_sec'])

/**
 * WLANInput from the merge patch of the form (edit) or the full values
 * (create). The passphrase is sent only when typed.
 */
export function toInput(patch: Record<string, unknown>, values: WlanForm, create: boolean): WLANInput {
  const keys = create ? Object.keys(values) : Object.keys(patch)
  const out: Record<string, unknown> = {}
  for (const key of keys) {
    if (key === 'psk') {
      if (values.psk) out.psk = values.psk
      continue
    }
    if (key === 'vlan') {
      out.vlan_id = values.vlan.tagged ? num(values.vlan.id) : 1
      continue
    }
    if (key in composite) {
      const value = composite[key](values)
      if (value !== undefined) out[key] = value
      continue
    }
    if (numericLeaves.has(key)) {
      out[key] = num(values[key as keyof WlanForm] as number | string | null)
      continue
    }
    out[key] = create ? values[key as keyof WlanForm] : patch[key]
  }
  if (create || 'security' in out) {
    // A WPA2 mode keeps its PMF choice; other modes fix PMF.
    if (values.security === 'wpa2_personal' || values.security === 'wpa2_enterprise') out.pmf = values.pmf
    else if (create) delete out.pmf
  }
  if (!create && 'pmf' in out && !(values.security === 'wpa2_personal' || values.security === 'wpa2_enterprise')) delete out.pmf
  return out as WLANInput
}

/** "Example (copy)" for Duplicate. */
export function duplicateForm(view: WLANView): WlanForm {
  const form = toForm(view)
  form.ssid = `${view.ssid} copy`.slice(0, 32)
  form.radius.auth = form.radius.auth.map((s) => ({ ...s, has_secret: false }))
  form.radius.acct = form.radius.acct.map((s) => ({ ...s, has_secret: false }))
  form.mpsk.keys = []
  return form
}
