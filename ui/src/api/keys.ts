// Query keys (design §7.5). Every query uses these builders so the SSE
// handlers and mutations can patch or invalidate exactly what they mean to.
// Site-scoped keys all start with ['site', id], so one invalidation covers a
// site's subtree.

export type EventFilters = {
  ap?: string
  client?: string
  kind?: string
  severity?: string
  range?: string
}

export const keys = {
  session: ['session'] as const,
  sites: ['sites'] as const,
  site: (id: string) => ['site', id] as const,
  siteAPs: (id: string) => ['site', id, 'aps'] as const,
  siteClients: (id: string, view: 'live' | 'recent' = 'live') => ['site', id, 'clients', view] as const,
  /** Infinite query; the default (unfiltered) list is `siteEvents(id, {})`. */
  siteEvents: (id: string, filters: EventFilters = {}) => ['site', id, 'events', filters] as const,
  /** Count of stream events that did not fit the cached first page ("New events" pill). */
  siteNewEvents: (id: string) => ['site', id, 'events-new'] as const,
  siteNetworks: (id: string) => ['site', id, 'networks'] as const,
  siteRF: (id: string) => ['site', id, 'rf'] as const,
  siteChanges: (id: string) => ['site', id, 'changes'] as const,
  /** One change record; under siteChanges so a new revision refreshes both. */
  siteChange: (id: string, rev: number | string) => ['site', id, 'changes', String(rev)] as const,
  siteRecommendations: (id: string) => ['site', id, 'recommendations'] as const,
  siteExpert: (id: string) => ['site', id, 'expert'] as const,
  siteNeighbours: (id: string, band: string = 'all') => ['site', id, 'neighbours', band] as const,
  /** Record<apId, PushEvent>: latest push lifecycle state per AP. */
  sitePush: (id: string) => ['site', id, 'push'] as const,
  /** One stored event with its `data` (the stream carries a summary only). */
  siteEvent: (id: string, eventId: string) => ['site', id, 'event', eventId] as const,
  /** History samples (site, or one AP with `ap`). */
  siteSamples: (id: string, params: { ap?: string; range?: string; step?: string } = {}) => ['site', id, 'samples', params] as const,
  ap: (apId: string) => ['ap', apId] as const,
  apTelemetry: (apId: string) => ['ap', apId, 'telemetry'] as const,
  apConfig: (apId: string) => ['ap', apId, 'config'] as const,
  /** GET /api/aps/$id/config?view= (under apConfig, so a push refreshes every view). */
  apConfigView: (apId: string, view: 'effective' | 'normalised' | 'applied') => ['ap', apId, 'config', view] as const,
  apExpert: (apId: string) => ['ap', apId, 'expert'] as const,
  apClients: (apId: string) => ['ap', apId, 'clients'] as const,
  network: (id: string) => ['network', id] as const,
  networkExpert: (id: string) => ['network', id, 'expert'] as const,
  client: (mac: string) => ['client', mac] as const,
  pending: ['pending'] as const,
  pendingCount: ['pending', 'count'] as const,
  /** GET /api/aps/pending, or the dismissed list with `dismissed: true`. */
  pendingList: (dismissed = false) => ['pending', 'list', { dismissed }] as const,
  system: ['system'] as const,
  systemSessions: ['system', 'sessions'] as const,
  systemTokens: ['system', 'tokens'] as const,
  systemMigration: ['system', 'migration'] as const,
  onboarding: ['onboarding'] as const,
} as const

/** staleTime defaults (§7.5): config objects 30 s (SSE invalidates), live views 10 s. */
export const stale = {
  config: 30_000,
  live: 10_000,
} as const
