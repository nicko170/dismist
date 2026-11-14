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
  /** Record<apId, PushEvent>: latest push lifecycle state per AP. */
  sitePush: (id: string) => ['site', id, 'push'] as const,
  ap: (apId: string) => ['ap', apId] as const,
  apTelemetry: (apId: string) => ['ap', apId, 'telemetry'] as const,
  apConfig: (apId: string) => ['ap', apId, 'config'] as const,
  network: (id: string) => ['network', id] as const,
  client: (mac: string) => ['client', mac] as const,
  pending: ['pending'] as const,
  pendingCount: ['pending', 'count'] as const,
  system: ['system'] as const,
} as const

/** staleTime defaults (§7.5): config objects 30 s (SSE invalidates), live views 10 s. */
export const stale = {
  config: 30_000,
  live: 10_000,
} as const
