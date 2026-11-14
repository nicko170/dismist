import { queryOptions } from '@tanstack/react-query'
import { api, type Query } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type {
  APDefaultsView,
  Eth1View,
  ExpertPatchView as GeneratedExpertPatchView,
  ManagementView,
  MigrationStatus,
  MutationResponse,
  PenaltyBoxView,
  ProtectionView,
  RecoveryView,
  SitePatch as GeneratedSitePatch,
  SiteView as SiteViewPhase1,
  SystemView,
} from '@/gen/api.gen'

// Site configuration (design §3.11, §4.9). The view and patch types are the
// generated contract (internal/api, tygo); the aliases keep the names the
// screens use.

export type {
  BlockedClient,
  ExpertSummary,
  SSHKeyView,
  SiteSSHInput,
  SyslogTarget,
} from '@/gen/api.gen'
export type Eth1Mode = Eth1View
export type ConfigRecovery = RecoveryView
export type PenaltyBox = PenaltyBoxView
export type APDefaults = APDefaultsView
export type Protection = ProtectionView
export type ManagementDefaults = ManagementView
/** GET /api/sites/$id (design §4.9 SiteView). */
export type SiteConfigView = SiteViewPhase1
/** PATCH /api/sites/$id: a JSON merge patch over the typed view (§4.8); `roaming.rotate_key` replaces the roaming key. */
export type SitePatch = GeneratedSitePatch & { roaming?: { rotate_key: boolean } }

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
    expert: { has_patch: false, field: 'expert', shadowed: [], partly_shadowed: [], overridden_on: {}, ...raw.expert },
    experimental_in_use: raw.experimental_in_use ?? [],
    typed: raw.typed ?? true,
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

export type ExpertPatchView = GeneratedExpertPatchView

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

export type MigrationState = MigrationStatus['state']
/** GET /api/system/migration; `report` is internal/migrate's V2Report, redacted. */
export type MigrationReport = MigrationStatus

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
