import type { Band, Disruption, ImpactAP, MutationResponse, SiteView } from '@/gen/api.gen'

// PROVISIONAL contract types for the phase 2 to 4 endpoints (design §4.3,
// §4.9), written from the design so the MSW mocks and the screens can be
// built before workstream C generates them. When src/gen/api.gen.ts gains a
// type of the same name, delete it here and add it to the re-export list in
// types.ts; tsc then shows every place the shapes differ. Field names are
// the design's snake_case JSON names.

// ---- WLANs (§3.9, §4.9 WLANView) ----

export type Security =
  | 'wpa2_personal'
  | 'wpa2_wpa3_personal'
  | 'wpa3_personal'
  | 'open'
  | 'owe'
  | 'owe_transition'
  | 'wpa2_enterprise'
  | 'wpa2_wpa3_enterprise'
  | 'wpa3_enterprise'

export type PMF = 'disabled' | 'optional' | 'required'
export type Isolation = 'none' | 'clients' | 'clients_and_lan'
export type RateProfile = 'compatible' | 'no_legacy' | 'high_density' | 'legacy_only'
export type QoSClass = '' | 'best_effort' | 'background' | 'video' | 'voice'

export type RadiusServerView = { host: string; port: number; has_secret: boolean }
export type RadiusServerInput = { host: string; port: number; secret?: string }

export type RadiusView = {
  auth: RadiusServerView[]
  acct: RadiusServerView[]
  mac_auth: boolean
  acct_interim_sec?: number
  nas_id?: string
  nas_ip?: string
  dynamic_vlan?: { enabled: boolean; type: 'standard' | 'airespace-interface-name'; vlan_ids: number[] } | null
  reauth_sec?: number
  reauth_on_session_timeout?: boolean
  coa?: { enabled: boolean; servers: { host: string; local_port: number; has_secret: boolean }[]; event_timestamp_optional: boolean } | null
}

export type WLANRoaming = { fast: 'none' | 'okc' | '11r'; ap_key_cache?: boolean; neighbor_report: boolean; bss_transition: boolean; ap_steering: boolean; bss_load: boolean }
export type WLANBroadcast = {
  limit: boolean
  allow_mdns: boolean
  allow_ssdp: boolean
  allow_ipv6_nd: boolean
  arp_filter: boolean
  mcast_to_ucast: boolean
  wireless_bridge_dhcp: boolean
}
export type RateLimit = { wlan_up: number | null; wlan_down: number | null; client_up: number | null; client_down: number | null }
export type MultiPSKKeyView = { id: string; name: string; has_passphrase: boolean; vlan_id: number | null; mac: string | null; expires: string | null }
export type ScheduleView = { enabled: boolean; days: Record<'mon' | 'tue' | 'wed' | 'thu' | 'fri' | 'sat' | 'sun', { from: string; to: string }[]> }

export type WLANView = {
  id: string
  site_id: string
  ssid: string
  enabled: boolean
  security: Security
  has_psk: boolean
  pmf: PMF | null
  radius: RadiusView | null
  bands: Band[]
  vlan_id: number
  hidden: boolean
  isolation: Isolation
  bssid_index: number
  owe_bssid_index?: number
  band_steering: boolean
  rates: RateProfile
  min_rssi?: Partial<Record<Band, number>> | null
  dtim: number
  idle_timeout_sec: number
  max_clients: number | null
  roaming: WLANRoaming
  broadcast: WLANBroadcast
  dhcp_only: boolean
  dhcp_dns_only: boolean
  wmm_disabled: boolean
  uapsd_disabled: boolean
  disable_11ax: boolean
  disable_11nac: boolean
  group_rekey_sec: number
  eapol_v1: boolean
  sae_h2e_only: boolean
  transition_disable: boolean
  owe_ssid?: string
  rate_limit?: RateLimit | null
  qos_class?: QoSClass
  honour_blocklist: boolean
  option82?: { enabled: boolean; circuit_id: string } | null
  mpsk?: { enabled: boolean; private: boolean; keys: MultiPSKKeyView[] } | null
  schedule?: ScheduleView | null
  /** Read-only facts (§3.9). */
  mdid?: string | null
  expert: { has_patch: boolean; shadowed: string[] }
  experimental_in_use: string[]
  live: { clients: number; aps_broadcasting: number; aps_total: number; bssids?: { ap_id: string; ap_name: string; band: Band; bssid: string; clients: number }[] }
  updated_rev: number
}

type WLANReadOnly = 'id' | 'site_id' | 'has_psk' | 'expert' | 'experimental_in_use' | 'live' | 'updated_rev' | 'bssid_index' | 'owe_bssid_index' | 'mdid' | 'radius' | 'mpsk'

/** POST /api/sites/$id/networks and PATCH …/networks/$nid (merge patch). Secrets are write-only. */
export type WLANInput = Partial<Omit<WLANView, WLANReadOnly>> & {
  psk?: string
  radius?: (Omit<Partial<RadiusView>, 'auth' | 'acct'> & { auth?: RadiusServerInput[]; acct?: RadiusServerInput[] }) | null
  mpsk?: { enabled: boolean; private: boolean; keys: (Omit<MultiPSKKeyView, 'has_passphrase'> & { passphrase?: string })[] } | null
}

// ---- Site settings (§3.11, §4.9 SiteView phase-2 fields) ----

export type SSHKeyView = { key: string; comment: string; type: string; fingerprint: string }
export type SyslogTarget = { protocol: 'udp' | 'tcp'; host: string; port: number; facility: number; severity: number; tag: string }
export type PowerPolicy = { mode: 'fixed'; dbm: number } | { mode: 'auto'; min: number; max: number }

export type BandProfileView = {
  enabled: boolean
  width: number
  channels: number[]
  dfs: boolean
  dfs_fallback: number
  power: PowerPolicy
  preamble: 'auto' | 'short' | 'long'
  advanced: {
    taf: 'default' | 'off' | 'on'
    twt: boolean
    antenna: 'default' | '4x4' | '3x3' | '2x2' | '1x1'
    min_probe_rssi: number | null
    mu_mimo: boolean | null
    txbf: boolean | null
  }
  /** From the regdomain table: the primaries valid per width (no 160 MHz on AP33 under AU/890). */
  valid_channels: { width: number; primaries: { channel: number; dfs: boolean }[] }[]
}

export type PlanRow = {
  ap_id: string
  ap_name: string
  band: Band
  mode: 'auto' | 'static'
  channel: number
  width: number
  power_dbm: number
  dfs_fallback: number | null
  source?: 'initial' | 'planner' | 'static'
  planned_at?: string | null
  actual: { channel: number; width: number; max_power_dbm: number; util: number; clients: number } | null
  status: 'match' | 'mismatch' | 'cac' | 'radio_missing' | 'offline'
}

export type Recommendation = { id: string; title: string; detail: string; scope: 'site' | 'ap' | 'wlan'; object_id: string; disruption: Disruption }

export type RFProfileView = {
  managed: boolean
  country: string
  ccrev: string | null
  band_24: BandProfileView
  band_5: BandProfileView
  scanning_radio: boolean | null
  plan: PlanRow[]
  recommendations: Recommendation[]
  /** An Auto band runs on the compile fallback because rf_plan is missing (§3.3). */
  fallback_aps?: string[]
  updated_rev?: number
}

export type RFProfileInput = Partial<Pick<RFProfileView, 'managed' | 'scanning_radio'>> & {
  band_24?: Partial<Omit<BandProfileView, 'valid_channels'>>
  band_5?: Partial<Omit<BandProfileView, 'valid_channels'>>
}

export type SiteSettingsView = SiteView & {
  ssh: { enabled: boolean; keys: SSHKeyView[]; readonly_keys: SSHKeyView[] }
  syslog: SyslogTarget[]
  management: { vlan_id: number; dns: string[]; ntp: string[] }
  ap_defaults: {
    led_brightness: number
    hostname_from_name: boolean
    keep_wlans_up_on_uplink_down: boolean
    poe_constrained: 'keep_radios' | 'disable_radios'
    wired_client_stats: boolean
    ble_disabled?: boolean
    eth1: unknown | null
    config_recovery: unknown | null
  }
  protection: {
    blocked_clients: { mac: string; note: string }[]
    proxy_arp: boolean
    arp_spoof_check: boolean
    penalty_box: { threshold: number; window_sec: number } | null
    dhcp_fingerprinting: boolean
  }
  rf: RFProfileView
  expert: { has_patch: boolean; shadowed: string[] }
}

/** PATCH /api/sites/$id: a merge patch over the site view; SSH keys are sent as lines. */
export type SiteSettingsInput = Partial<Omit<SiteSettingsView, 'id' | 'counts' | 'updated_rev' | 'rf' | 'expert' | 'ssh' | 'ccrev'>> & {
  ssh?: { enabled?: boolean; keys?: string[]; readonly_keys?: string[] }
  rf?: RFProfileInput
}

// ---- Expert patches (§4.4, §7.4) ----

export type ExpertScope = 'site' | 'ap' | 'wlan'
export type ExpertPatchView = {
  scope: ExpertScope
  object_id: string
  patch: Record<string, unknown>
  shadowed: string[]
  partly_shadowed: string[]
  ignored_keys: string[]
  /** Firmware paths for completion and the forbidden-path mirror (client conveniences; the server decides). */
  paths?: string[]
  forbidden?: string[]
  legacy?: boolean
  updated_rev: number
}

// ---- RF plan and neighbours (§3.10, phase 3) ----

export type PlanProposal = {
  inputs: { aps: number; scans_age_s: Record<string, number | null> }
  changes: {
    ap_id: string
    band: Band
    from: { channel: number; power_dbm: number }
    to: { channel: number; power_dbm: number; dfs_fallback: number | null }
    cost_before: number
    cost_after: number
    keep: boolean
    reason: string
    disruption: 'radio_bounce' | 'none'
  }[]
}
export type PlanResponse = MutationResponse<PlanProposal>

export type Neighbour = {
  ssid: string
  bssid: string
  band: Band
  channel: number
  width: number
  rssi: number
  seen_by: string
  station_count: number | null
  util: number | null
  last_seen: string
}

// ---- Changes and restore (§3.13, §4.11) ----

export type ChangeSummary = {
  revision: number
  at: string
  actor: { kind: 'session' | 'token' | 'system'; name: string }
  kind?: 'config' | 'action'
  summary: string
  affected_aps: { id: string; name: string; state: string }[]
}
export type ChangePage = { changes: ChangeSummary[]; next_before: string | null }
export type ChangeRecord = ChangeSummary & {
  endpoint: string
  objects: { kind: 'site' | 'network' | 'ap'; id: string; name: string }[]
  /** Redacted typed diff of the stored objects. */
  diff: { object: string; path: string; from: unknown; to: unknown }[]
  /** Per-AP compiled diff, redacted. */
  aps: Pick<ImpactAP, 'id' | 'name' | 'diff'>[]
  results: Record<string, { state: string; at: string | null; error: string }>
}
export type RestoreInput = { keep_new_wlans?: boolean; secrets?: Record<string, { value?: string; keep_current?: boolean }> }

// ---- Actions (§3.5, §5.6, phase 2 to 3) ----

export type APAction = 'locate' | 'unlocate' | 'push-config' | 'reboot' | 'restart-radios'
export type ActionInput = { minutes?: number; band?: Band | 'both' }
export type ActionResult = { action: string; state: 'accepted' | 'done'; until?: string; action_id?: string }

// ---- Phase 4 ----

export type RevealView = { psk: string; wifi_qr: string }
export type ClientMeta = { alias: string | null; notes: string }

// ---- History samples (§3.3, §4.1) ----

export type SampleRadio = { band: Band; channel: number; width: number; util_all: number; noise_dbm: number; clients: number; max_power_dbm: number }
export type Sample = { at: string; ap_id?: string; clients: number; clients_24: number; clients_5: number; radios?: SampleRadio[] }
export type SamplesView = { range: string; step: string; samples: Sample[] }

// ---- Migration (§4.7, /system/migration) ----

export type MigrationAPReport = {
  ap_id: string
  ap_name: string
  equal: boolean
  hash_before: string
  hash_after: string
  normaliser_version: number
  bssid_indexes: { network_id: string; ssid: string; index: number; source: 'measured' | 'predicted' }[]
  local_files: 'absent' | 'present' | 'unknown'
  mismatches: string[]
}
export type MigrationReport = {
  state: 'not_run' | 'committed' | 'legacy'
  reason: string
  at: string | null
  lifted: { kind: 'site' | 'network' | 'ap'; id: string; name: string; fields: string[]; residual: Record<string, unknown> | null }[]
  aps: MigrationAPReport[]
  compat: number
  can_finalise: boolean
}

