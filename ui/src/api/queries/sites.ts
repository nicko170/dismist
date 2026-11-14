import { queryOptions } from '@tanstack/react-query'
import { api, type Query } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { MutationResponse, SiteView as SiteViewPhase1, SystemView } from '@/gen/api.gen'
import type { RFProfileView } from './rf'

// Site configuration (design §3.11, §4.9). PROVISIONAL view types: written by
// hand from §4.3/§4.9 until workstream C adds the phase-2 typed fields to
// internal/api and regenerates gen/api.gen.ts. Every field added here beyond
// §4.9 is listed in F's integration report as a request to C.

export type SSHKeyView = { key: string; comment: string; type: string; fingerprint: string }
export type SSHKeyInput = { key: string; comment: string }

export type SyslogTarget = {
  protocol: 'udp' | 'tcp'
  host: string
  port: number
  facility: number
  severity: number
  tag: string
}

/** eth1 behaviour (§3.6 Ports, §3.11 AP defaults). Experimental. */
export type Eth1Mode = {
  mode: 'same_as_uplink' | 'access' | 'trunk' | 'disabled'
  vlan_id: number | null
  vlans: number[]
  pvid: number | null
}

export type ConfigRecovery = { enabled: boolean; minutes: number }
export type PenaltyBox = { threshold: number; window_sec: number }
export type BlockedClient = { mac: string; note: string }

/** Paths of a typed option that an expert patch overrides. */
export type ShadowInfo = { key: string; paths: string[] }
/** A site option overridden on some APs (§3.0 "Shadowed fields", §4.4 cross scope). */
export type CrossScopeShadow = { key: string; kind: 'patch' | 'override'; aps: { id: string; name: string }[] }

export type ExpertSummary = {
  has_patch: boolean
  /** Option keys whose every firmware path the scope's patch overrides. */
  shadowed: string[]
  partly_shadowed?: ShadowInfo[]
  cross_scope?: CrossScopeShadow[]
}

export type APDefaults = {
  led_brightness: number
  hostname_from_name: boolean
  keep_wlans_up_on_uplink_down: boolean
  poe_constrained: 'keep_radios' | 'disable_radios'
  wired_client_stats: boolean
  eth1: Eth1Mode | null
  config_recovery: ConfigRecovery | null
  ble_disabled: boolean
}

export type Protection = {
  blocked_clients: BlockedClient[]
  proxy_arp: boolean
  arp_spoof_check: boolean
  penalty_box: PenaltyBox | null
  dhcp_fingerprinting: boolean
}

export type ManagementDefaults = { vlan_id: number; dns: string[]; ntp: string[] }

/** GET /api/sites/$id once schema 2 is live (§4.9 SiteView). */
export type SiteConfigView = SiteViewPhase1 & {
  ssh: { enabled: boolean; keys: SSHKeyView[]; readonly_keys: SSHKeyView[] }
  syslog: SyslogTarget[]
  management: ManagementDefaults
  ap_defaults: APDefaults
  protection: Protection
  rf?: RFProfileView
  /** The site roaming key: whether one exists and whether any WLAN uses it (never the key). */
  roaming?: { key_set: boolean; in_use: boolean; wlans?: string[] }
  expert: ExpertSummary
  /** Option keys (or key=value) of experimental values stored in this site. */
  experimental_in_use?: string[]
}

/** PATCH /api/sites/$id: a JSON merge patch over the typed view (§4.8). */
export type SitePatch = {
  name?: string
  country?: string
  timezone?: string
  experimental?: boolean
  ssh?: { enabled?: boolean; keys?: SSHKeyInput[]; readonly_keys?: SSHKeyInput[] }
  syslog?: SyslogTarget[]
  management?: Partial<ManagementDefaults>
  ap_defaults?: Partial<APDefaults>
  protection?: Partial<Protection>
  roaming?: { rotate_key?: boolean }
}

export type MutationOptions = { dryRun?: boolean; ifMatch?: number; signal?: AbortSignal }

/** `?dry_run=1` on every config mutation (§4.8). */
export function dryRunQuery(options: MutationOptions, extra: Query = {}): Query {
  return options.dryRun ? { ...extra, dry_run: 1 } : extra
}

const sitePath = (siteId: string) => `/api/sites/${encodeURIComponent(siteId)}`

/** Fills fields an older server (phase 1) does not send, so screens never crash on them. */
export function normaliseSiteView(raw: Partial<SiteConfigView> & { id: string }): SiteConfigView {
  return {
    name: raw.id,
    country: '',
    timezone: '',
    counts: { aps: 0, aps_online: 0, clients: 0, wlans: 0, pending_aps: 0 },
    ccrev: null,
    experimental: false,
    updated_rev: 0,
    ...raw,
    ssh: { enabled: false, keys: [], readonly_keys: [], ...raw.ssh },
    syslog: raw.syslog ?? [],
    management: { vlan_id: 1, dns: [], ntp: [], ...raw.management },
    ap_defaults: {
      led_brightness: 255,
      hostname_from_name: false,
      keep_wlans_up_on_uplink_down: false,
      poe_constrained: 'disable_radios',
      wired_client_stats: false,
      eth1: null,
      config_recovery: null,
      ble_disabled: false,
      ...raw.ap_defaults,
    },
    protection: {
      blocked_clients: [],
      proxy_arp: true,
      arp_spoof_check: false,
      penalty_box: null,
      dhcp_fingerprinting: false,
      ...raw.protection,
    },
    expert: { has_patch: false, shadowed: [], ...raw.expert },
  } as SiteConfigView
}

export function siteQuery(siteId: string) {
  return queryOptions({
    queryKey: keys.site(siteId),
    queryFn: async ({ signal }) =>
      normaliseSiteView(await api.get<SiteConfigView>(sitePath(siteId), { signal })),
    staleTime: stale.config,
  })
}

export function patchSite(siteId: string, body: SitePatch, options: MutationOptions = {}) {
  return api.patch<MutationResponse<SiteConfigView>>(sitePath(siteId), body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

export function deleteSite(siteId: string, options: MutationOptions = {}) {
  return api.delete<MutationResponse<null> | undefined>(sitePath(siteId), {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
  })
}

// ---- expert patches (§4.4, §4.9 ExpertPatchView) ----

export type ExpertScope = 'site' | 'ap' | 'wlan'

export type ExpertPatchView = {
  scope: ExpertScope
  object_id: string
  /** Redacted; "[redacted]" round-trips to the stored secret. */
  patch: Record<string, unknown>
  shadowed: string[]
  partly_shadowed: string[]
  ignored_keys: string[]
  updated_rev: number
}

export type ExpertPatchInput = { patch: Record<string, unknown> }

export function siteExpertQuery(siteId: string) {
  return queryOptions({
    queryKey: [...keys.site(siteId), 'expert'] as const,
    queryFn: ({ signal }) => api.get<ExpertPatchView>(`${sitePath(siteId)}/expert`, { signal }),
    staleTime: stale.config,
  })
}

export function putSiteExpert(siteId: string, body: ExpertPatchInput, options: MutationOptions = {}) {
  return api.put<MutationResponse<ExpertPatchView>>(`${sitePath(siteId)}/expert`, body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

// ---- schema-2 migration state (§4.7, §3.14 /system/migration) ----

export type MigrationState = SystemView['migration']['state']

export type MigrationReport = {
  state: MigrationState
  at: string | null
  reason: string
  /** JSON paths that failed verification (secrets redacted). */
  paths: string[]
  report: {
    lifted: { scope: string; object_id: string; name: string; values: Record<string, unknown> }[]
    residuals: { scope: string; object_id: string; name: string; patch: Record<string, unknown> }[]
    bssid_indexes: { network_id: string; ssid: string; index: number; source: 'measured' | 'predicted' }[]
    aps: { id: string; name: string; verified: boolean; local_files: 'none' | 'present' | 'unknown'; note: string }[]
  } | null
  compat: number
  finalise: { allowed: boolean; reason: string }
}

export const migrationQuery = queryOptions({
  queryKey: [...keys.system, 'migration'] as const,
  queryFn: ({ signal }) => api.get<MigrationReport>('/api/system/migration', { signal }),
  staleTime: stale.config,
})

export function retryMigration(options: MutationOptions = {}) {
  return api.post<MigrationReport>('/api/system/migration/retry', undefined, { query: dryRunQuery(options) })
}

export function finaliseMigration(options: MutationOptions = {}) {
  return api.post<MigrationReport>('/api/system/migration/finalise', undefined, { query: dryRunQuery(options) })
}

/**
 * Typed config editing needs a committed schema-2 migration (§3.0 "Legacy
 * mode"). Shares the ['system'] key with workstream E's System page.
 */
export const systemMigrationQuery = queryOptions({
  queryKey: keys.system,
  queryFn: ({ signal }) => api.get<SystemView>('/api/system', { signal }),
  staleTime: stale.config,
})
