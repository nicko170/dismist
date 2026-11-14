import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type {
  APConfigView,
  APDetail,
  APOverrides,
  Band,
  ConfigView,
  MutationResponse,
  PlannedRadio,
  RadioOverride,
} from '@/gen/api.gen'
import type { Eth1Mode, ExpertPatchInput, ExpertPatchView } from './sites'
import { dryRunQuery, type MutationOptions } from './sites'

// AP settings, overrides, expert patch and protocol-feature mutations
// (design §3.6, §3.7, §4.9). The AP detail itself is workstream E's query;
// this file shares its ['ap', id] key and tolerates a phase-1 server that does
// not send the phase-2 fields yet.

/** The protocol features of §5.7 with their fixed names, in display order. */
export const PROTOCOL_FEATURES = [
  { name: 'subscribe_reply', subject: 'ep-subscribe', label: 'Reply to ep-subscribe', help: 'Answers the AP’s subscription request so it stops retrying every minute.', confidence: 'confirmed' },
  { name: 'file_discard', subject: 'ep-file', label: 'Discard offered crash reports (ep-file)', help: 'Tells the AP not to upload crash reports, and records an info event for each one offered.', confidence: 'likely' },
  { name: 'site_rest_404', subject: 'wlc-site-rest', label: 'Answer wlc-site-rest with 404', help: 'Replies at once instead of letting the AP’s roaming lookup time out after 10 s.', confidence: 'needs-live-test' },
  { name: 'pong2', subject: 'ping2', label: 'Reply to ping2 with Pong2', help: 'Lets the AP measure the controller round trip time.', confidence: 'likely' },
  { name: 'config_change_notify', subject: 'ep-config-change', label: 'Push config with ep-config-change', help: 'Asks the AP to fetch a new config instead of reconnecting it. Falls back to a reconnect if the AP does not fetch within 15 s.', confidence: 'likely' },
  { name: 'ap_cmd', subject: 'ap-cmd-rx', label: 'AP commands (ap-cmd-rx)', help: 'Reboot, radio restart and client disconnect over the controller session. The wire format is unverified; off until the lead tests it.', confidence: 'needs-live-test' },
] as const

export type ProtocolFeatureName = (typeof PROTOCOL_FEATURES)[number]['name']

export type ManagementOverride = {
  ip_mode: 'dhcp' | 'static'
  address: string
  prefix: number | null
  gateway: string
  dns: string[]
  vlan: { mode: 'inherit' | 'untagged' | 'tagged'; id: number | null }
  ntp: string[] | null
}

/** APDetail with every phase-2 field present (defaults filled for older servers). */
export type APSettingsView = APDetail & {
  overrides: APOverrides & { wired_client_stats?: boolean | null }
  rf_plan: { [band in Band]: (PlannedRadio & { source?: string; reason?: string; planned_at?: string }) | null }
  protocol_features: Record<string, boolean>
  /** When each feature last changed (optional; shown when present). */
  protocol_features_at?: Record<string, string>
  /** The site's DNS mode makes the source pin required (§3.6 General). */
  dns_mode?: boolean
}

const emptyRF: { [band in Band]: RadioOverride | null } = { '2.4': null, '5': null }

export function normaliseAPDetail(raw: Partial<APSettingsView> & { id: string }): APSettingsView {
  const overrides = (raw.overrides ?? {}) as Partial<APSettingsView['overrides']>
  return {
    ...(raw as APSettingsView),
    flags: raw.flags ?? [],
    protocol_features: raw.protocol_features ?? {},
    local_files: raw.local_files ?? null,
    radios_detail: raw.radios_detail ?? [],
    wlans_live: raw.wlans_live ?? [],
    overrides: {
      led_brightness: overrides.led_brightness ?? null,
      ssh_enabled: overrides.ssh_enabled ?? null,
      rf: { ...emptyRF, ...overrides.rf },
      management: overrides.management ?? null,
      eth1: overrides.eth1 ?? null,
      wired_client_stats: overrides.wired_client_stats ?? null,
    },
    rf_plan: { '2.4': null, '5': null, ...raw.rf_plan },
    capabilities: raw.capabilities ?? { config_change_notify: null, ap_cmd: false, pong2: false },
  }
}

const apPath = (apId: string) => `/api/aps/${encodeURIComponent(apId)}`

export function apSettingsQuery(apId: string) {
  return queryOptions({
    queryKey: keys.ap(apId),
    queryFn: async ({ signal }) => normaliseAPDetail(await api.get<APSettingsView>(apPath(apId), { signal })),
    staleTime: stale.config,
  })
}

/** PATCH /api/aps/$id: only the section's fields; If-Match is required (§4.9). */
export type APPatch = {
  name?: string
  site_id?: string
  management_ip?: string
  secret?: string
  protocol_features?: Record<string, boolean>
  overrides?: {
    led_brightness?: number | null
    ssh_enabled?: boolean | null
    rf?: Partial<Record<Band, Partial<RadioOverride> | null>>
    management?: ManagementOverride | null
    eth1?: Eth1Mode | null
    wired_client_stats?: boolean | null
  }
}

export function patchAP(apId: string, body: APPatch, options: MutationOptions = {}) {
  return api.patch<MutationResponse<APSettingsView>>(apPath(apId), body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

export function apExpertQuery(apId: string) {
  return queryOptions({
    queryKey: [...keys.ap(apId), 'expert'] as const,
    queryFn: ({ signal }) => api.get<ExpertPatchView>(`${apPath(apId)}/expert`, { signal }),
    staleTime: stale.config,
  })
}

export function putAPExpert(apId: string, body: ExpertPatchInput, options: MutationOptions = {}) {
  return api.put<MutationResponse<ExpertPatchView>>(`${apPath(apId)}/expert`, body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

/** One compile-trace entry (§3.7): which layer last set a typed option. */
export type TraceEntry = {
  key: string
  layer: 'template' | 'site' | 'site_expert' | 'ap' | 'ap_expert' | 'wlan' | 'wlan_expert' | 'protected' | string
  shadowed: boolean
  paths?: string[]
}

export type APConfigDocument = APConfigView & { trace?: TraceEntry[]; warnings?: { code: string; message: string }[] }

export function apConfigQuery(apId: string, view: ConfigView) {
  return queryOptions({
    queryKey: [...keys.apConfig(apId), view] as const,
    queryFn: ({ signal }) => api.get<APConfigDocument>(`${apPath(apId)}/config`, { query: { view }, signal }),
    staleTime: stale.config,
  })
}

export type ActionResult = { action: string; state: 'accepted' | 'done'; until?: string; action_id?: string }

/** LED locate (experimental, phase 3): LED.Locator through a push, cleared by a timer. */
export function locateAP(apId: string, minutes: number) {
  return api.post<ActionResult>(`${apPath(apId)}/actions/locate`, { minutes })
}

export function unlocateAP(apId: string) {
  return api.post<ActionResult>(`${apPath(apId)}/actions/unlocate`, {})
}
