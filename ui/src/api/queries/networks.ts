import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { Band, MutationResponse } from '@/gen/api.gen'
import { dryRunQuery, type ExpertPatchInput, type ExpertPatchView, type ExpertSummary, type MutationOptions } from './sites'

// WLANs (design §3.8, §3.9, §4.3 Network, §4.9 WLANView). PROVISIONAL types
// from §4.9 ("…remaining advanced fields as in §4.3, snake_case…") until C
// regenerates gen/api.gen.ts with the phase-2 DTOs.

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
export type RatesProfile = 'compatible' | 'no_legacy' | 'high_density' | 'legacy_only'
export type QoSClass = 'none' | 'best_effort' | 'background' | 'video' | 'voice'
export type FastRoaming = 'none' | 'okc' | '11r'

/** A RADIUS server as the API shows it: the secret is write-only. */
export type RadiusServerView = { host: string; port: number; has_secret: boolean }
export type RadiusServerInput = { host: string; port: number; secret?: string }

export type CoAServer = { host: string; local_port: number; has_secret?: boolean; secret?: string }

export type RadiusView = {
  auth: RadiusServerView[]
  acct: RadiusServerView[]
  mac_auth: boolean
  acct_interim_sec: number | null
  nas_id: string
  nas_ip: string
  dynamic_vlan: { enabled: boolean; type: 'standard' | 'airespace-interface-name'; vlan_ids: number[] } | null
  reauth_sec: number | null
  reauth_on_session_timeout: boolean
  coa: { enabled: boolean; servers: CoAServer[]; event_timestamp_optional: boolean } | null
}

export type MPSKKeyView = {
  id: string
  name: string
  has_passphrase: boolean
  vlan_id: number | null
  mac: string
  expires: string | null
}

export type MPSKView = { enabled: boolean; private: boolean; keys: MPSKKeyView[] }

/** Per-weekday hour ranges in the site timezone (§3.9 Schedule). */
export type ScheduleView = {
  enabled: boolean
  /** mon..sun → list of "HH:MM-HH:MM" ranges; an empty list means off all day. */
  days: Record<'mon' | 'tue' | 'wed' | 'thu' | 'fri' | 'sat' | 'sun', string[]>
}

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
  /** 1 = untagged. */
  vlan_id: number
  hidden: boolean
  isolation: Isolation
  bssid_index: number
  owe_bssid_index?: number | null
  band_steering: boolean
  rates: RatesProfile
  dtim: number
  idle_timeout_sec: number
  max_clients: number | null
  roaming: {
    fast: FastRoaming
    ap_key_cache: boolean
    neighbor_report: boolean
    bss_transition: boolean
    ap_steering: boolean
    bss_load: boolean
  }
  broadcast: {
    limit: boolean
    allow_mdns: boolean
    allow_ssdp: boolean
    allow_ipv6_nd: boolean
    arp_filter: boolean
    mcast_to_ucast: boolean
    wireless_bridge_dhcp: boolean
  }
  dhcp_only: boolean
  dhcp_dns_only: boolean
  group_rekey_sec: number
  eapol_v1: boolean
  sae_h2e_only: boolean
  transition_disable: boolean
  owe_ssid: string
  min_rssi: Partial<Record<Band, number>> | null
  wmm_disabled: boolean
  uapsd_disabled: boolean
  disable_11ax: boolean
  disable_11nac: boolean
  rate_limit: { wlan_up: number | null; wlan_down: number | null; client_up: number | null; client_down: number | null } | null
  qos_class: QoSClass
  honour_blocklist: boolean
  option82: { enabled: boolean; circuit_id: string } | null
  mpsk: MPSKView | null
  schedule: ScheduleView | null
  /** 802.11r mobility domain (read-only). */
  mdid?: string | null
  expert: ExpertSummary
  experimental_in_use: string[]
  live: { clients: number; aps_broadcasting: number; aps_total: number }
  /** Per AP and band, from field 32 (read-only facts). */
  live_vaps?: { ap_id: string; ap_name: string; band: Band; bssid: string; dev: string; clients: number }[]
  updated_rev: number
}

/** POST body, and the PATCH merge patch: read-only fields removed, secrets write-only. */
export type WLANInput = Partial<
  Omit<WLANView, 'id' | 'site_id' | 'has_psk' | 'bssid_index' | 'owe_bssid_index' | 'expert' | 'experimental_in_use' | 'live' | 'live_vaps' | 'mdid' | 'updated_rev' | 'radius' | 'mpsk'>
> & {
  psk?: string
  radius?: Partial<Omit<RadiusView, 'auth' | 'acct'>> & { auth?: RadiusServerInput[]; acct?: RadiusServerInput[] } | null
  mpsk?: { enabled?: boolean; private?: boolean; keys?: { id?: string; name: string; passphrase?: string; vlan_id: number | null; mac: string; expires: string | null }[] } | null
}

export type RevealView = { psk: string; wifi_qr: string; keys?: { id: string; name: string; psk: string; wifi_qr: string }[] }

const networksPath = (siteId: string) => `/api/sites/${encodeURIComponent(siteId)}/networks`
const networkPath = (siteId: string, id: string) => `${networksPath(siteId)}/${encodeURIComponent(id)}`

export function networksQuery(siteId: string) {
  return queryOptions({
    queryKey: keys.siteNetworks(siteId),
    queryFn: ({ signal }) => api.get<WLANView[]>(networksPath(siteId), { signal }),
    staleTime: stale.config,
  })
}

export function networkQuery(siteId: string, id: string) {
  return queryOptions({
    queryKey: keys.network(id),
    queryFn: ({ signal }) => api.get<WLANView>(networkPath(siteId, id), { signal }),
    staleTime: stale.config,
  })
}

export function createNetwork(siteId: string, body: WLANInput, options: MutationOptions = {}) {
  return api.post<MutationResponse<WLANView>>(networksPath(siteId), body, {
    query: dryRunQuery(options),
    signal: options.signal,
  })
}

export function patchNetwork(siteId: string, id: string, body: WLANInput, options: MutationOptions = {}) {
  return api.patch<MutationResponse<WLANView>>(networkPath(siteId, id), body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

export function deleteNetwork(siteId: string, id: string, options: MutationOptions = {}) {
  return api.delete<MutationResponse<null>>(networkPath(siteId, id), {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
  })
}

export function networkExpertQuery(siteId: string, id: string) {
  return queryOptions({
    queryKey: [...keys.network(id), 'expert'] as const,
    queryFn: ({ signal }) => api.get<ExpertPatchView>(`${networkPath(siteId, id)}/expert`, { signal }),
    staleTime: stale.config,
  })
}

export function putNetworkExpert(siteId: string, id: string, body: ExpertPatchInput, options: MutationOptions = {}) {
  return api.put<MutationResponse<ExpertPatchView>>(`${networkPath(siteId, id)}/expert`, body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

/** Phase 4: admin scope, audit-logged on the server. */
export function revealNetwork(siteId: string, id: string) {
  return api.post<RevealView>(`${networkPath(siteId, id)}/reveal`)
}
