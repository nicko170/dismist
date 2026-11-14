import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { ApiError, toApiError, networkError } from '@/api/errors'
import { keys, stale } from '@/api/keys'
import type { AuthSessionView, MigrationState, SystemView, TokenCreated, TokenInput, TokenView } from '@/gen/api.gen'

// System page data (design §3.14, §4.9; workstream E): controller, AP
// endpoint, database, backups, API tokens, signed-in sessions, migration.

export const systemKeys = {
  tokens: [...keys.system, 'tokens'] as const,
  sessions: [...keys.system, 'sessions'] as const,
  migration: [...keys.system, 'migration'] as const,
}

// ---- provisional: GET /api/system/migration (design §3.14, §4.7) ----

export type MigrationObjectReport = {
  scope: 'site' | 'ap' | 'wlan'
  id: string
  name: string
  /** Typed values lifted from the effective config (option key → value). */
  lifted: Record<string, unknown>
  /** The residual expert patch stored for this object ({} when none). */
  residual: unknown
}

export type MigrationAPReport = {
  id: string
  name: string
  verified: boolean
  local_files: 'none' | 'unknown' | 'present' | string
  bssid_index: 'measured' | 'predicted' | string
  mismatches: string[]
}

export type MigrationView = {
  state: MigrationState
  reason: string
  at: string | null
  /** In legacy mode: the paths that failed verification (secrets redacted). */
  paths: string[]
  schema: number
  compat: number
  report: { objects: MigrationObjectReport[]; aps: MigrationAPReport[]; normaliser_version: number | null } | null
  /** Finalise (phase 4) is offered only after M2 has run 30 days. */
  finalise_available: boolean
  finalise_after: string | null
  finalised: boolean
}

const str = (value: unknown, fallback = ''): string => (typeof value === 'string' ? value : fallback)
const arr = <T>(value: unknown): T[] => (Array.isArray(value) ? (value as T[]) : [])

type Row = Record<string, unknown>
const obj = (value: unknown): Row => (value && typeof value === 'object' && !Array.isArray(value) ? (value as Row) : {})

/**
 * The lift report is internal/migrate's V2Report (redacted): sites,
 * networks and APs, each with its typed lift and residual expert patch, and
 * per AP whether its effective config verified equal. A report already in
 * the screen's shape (objects) passes through.
 */
export function normaliseV2Report(report: Row): NonNullable<MigrationView['report']> {
  if (Array.isArray(report.objects)) {
    return {
      objects: arr<MigrationObjectReport>(report.objects),
      aps: arr<MigrationAPReport>(report.aps).map((ap) => ({ ...ap, mismatches: arr<string>(ap.mismatches) })),
      normaliser_version: typeof report.normaliser_version === 'number' ? report.normaliser_version : null,
    }
  }
  const networks = arr<Row>(report.networks)
  const objects: MigrationObjectReport[] = [
    ...arr<Row>(report.sites).map((site) => ({
      scope: 'site' as const,
      id: str(site.id),
      name: str(site.name, str(site.id)),
      lifted: { ...obj(site.typed), rf_managed: Boolean(site.rf_managed) },
      residual: site.residual ?? {},
    })),
    ...networks.map((network) => ({
      scope: 'wlan' as const,
      id: str(network.id),
      name: str(network.ssid, str(network.id)),
      lifted: { security: network.security, bssid_index: network.bssid_index, ...obj(network.typed) },
      residual: network.residual ?? {},
    })),
    ...arr<Row>(report.aps).map((ap) => ({
      scope: 'ap' as const,
      id: str(ap.id),
      name: str(ap.name, str(ap.id)),
      lifted: ap.rf_override ? { rf_override: ap.rf_override } : {},
      residual: ap.residual ?? {},
    })),
  ]
  const bssidSource = networks.some((n) => n.bssid_source === 'predicted') ? 'predicted' : networks.length ? 'measured' : ''
  const aps: MigrationAPReport[] = arr<Row>(report.aps).map((ap) => ({
    id: str(ap.id),
    name: str(ap.name, str(ap.id)),
    verified: Boolean(ap.equal),
    local_files: str(ap.local_files, 'unknown'),
    bssid_index: bssidSource,
    mismatches: arr<string>(ap.mismatch),
  }))
  return { objects, aps, normaliser_version: typeof report.normaliser_version === 'number' ? report.normaliser_version : null }
}

export function normaliseMigration(body: unknown, system?: SystemView): MigrationView {
  const row = (body ?? {}) as Record<string, unknown>
  const report = (row.report && typeof row.report === 'object' ? row.report : null) as Record<string, unknown> | null
  const state = (['not_run', 'committed', 'legacy'] as const).includes(row.state as MigrationState)
    ? (row.state as MigrationState)
    : (system?.migration.state ?? 'not_run')
  return {
    state,
    reason: str(row.reason, system?.migration.reason ?? ''),
    at: str(row.at) || null,
    paths: arr<string>(row.paths).filter((p) => typeof p === 'string'),
    schema: typeof row.schema === 'number' ? row.schema : (system?.db.schema ?? 1),
    compat: typeof row.compat === 'number' ? row.compat : (system?.db.compat ?? 1),
    report: report ? normaliseV2Report(report) : null,
    finalise_available: Boolean(row.finalise_available ?? row.can_finalise),
    finalise_after: str(row.finalise_after) || null,
    finalised: Boolean(row.finalised),
  }
}

// ---- queries ----

export const systemQuery = queryOptions({
  queryKey: keys.system,
  queryFn: ({ signal }) => api.get<SystemView>('/api/system', { signal }),
  staleTime: stale.live,
  refetchInterval: 30_000,
})

export const tokensQuery = queryOptions({
  queryKey: systemKeys.tokens,
  queryFn: async ({ signal }) => {
    const body = await api.get<TokenView[] | { tokens?: TokenView[] }>('/api/system/tokens', { signal })
    return Array.isArray(body) ? body : (body?.tokens ?? [])
  },
  staleTime: stale.config,
})

export const sessionsQuery = queryOptions({
  queryKey: systemKeys.sessions,
  queryFn: async ({ signal }) => {
    const body = await api.get<AuthSessionView[] | { sessions?: AuthSessionView[] }>('/api/system/sessions', { signal })
    const rows = Array.isArray(body) ? body : (body?.sessions ?? [])
    return rows.toSorted((a, b) => Number(b.current) - Number(a.current) || Date.parse(b.last_used ?? b.created) - Date.parse(a.last_used ?? a.created))
  },
  staleTime: stale.live,
})

/** Migration state; a server without the endpoint (phase 1) answers from /api/system. */
export const migrationQuery = queryOptions({
  queryKey: systemKeys.migration,
  queryFn: async ({ signal }) => {
    const system = await api.get<SystemView>('/api/system', { signal }).catch(() => undefined)
    try {
      return normaliseMigration(await api.get<unknown>('/api/system/migration', { signal }), system)
    } catch (error) {
      if (error instanceof ApiError && (error.status === 404 || error.status === 501) && system) return normaliseMigration({}, system)
      throw error
    }
  },
  staleTime: stale.live,
})

// ---- mutations ----

export function useCreateToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: TokenInput) => api.post<TokenCreated>('/api/system/tokens', input),
    onSuccess: () => void qc.invalidateQueries({ queryKey: systemKeys.tokens }),
  })
}

export function useRevokeToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.delete<void>(`/api/system/tokens/${encodeURIComponent(id)}`),
    onSuccess: (_r, id) => {
      qc.setQueryData<TokenView[]>(systemKeys.tokens, (rows) => rows?.filter((row) => row.id !== id))
      void qc.invalidateQueries({ queryKey: systemKeys.tokens })
    },
  })
}

export function useRevokeSession() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.delete<void>(`/api/system/sessions/${encodeURIComponent(id)}`),
    onSuccess: (_r, id) => {
      qc.setQueryData<AuthSessionView[]>(systemKeys.sessions, (rows) => rows?.filter((row) => row.id !== id))
      void qc.invalidateQueries({ queryKey: systemKeys.sessions })
    },
  })
}

/** POST /api/system/sessions/revoke-all ("Sign out everywhere", including this session). */
export function useRevokeAllSessions() {
  return useMutation({ mutationFn: () => api.post<void>('/api/system/sessions/revoke-all', {}) })
}

/** POST /api/system/migration/retry[?dry_run=1] (from legacy mode, no restart). */
export function useMigrationRetry() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ dryRun }: { dryRun: boolean }) => api.post<unknown>('/api/system/migration/retry', {}, { query: { dry_run: dryRun ? 1 : undefined } }),
    onSuccess: (_r, { dryRun }) => {
      if (dryRun) return
      void qc.invalidateQueries({ queryKey: keys.system })
    },
  })
}

/** POST /api/system/migration/finalise (phase 4): clears legacy keys, compat 2. */
export function useMigrationFinalise() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<unknown>('/api/system/migration/finalise', {}),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.system }),
  })
}

/**
 * GET /api/system/backup: streams a consistent DB copy (contains PSKs and AP
 * secrets). Downloaded through fetch so errors (429 one-at-a-time, 401)
 * surface as ApiError instead of a broken file.
 */
export async function downloadBackup(): Promise<string> {
  let response: Response
  try {
    response = await fetch('/api/system/backup', { credentials: 'same-origin', cache: 'no-store' })
  } catch (cause) {
    throw networkError(cause)
  }
  if (!response.ok) throw await toApiError(response)
  const disposition = response.headers.get('Content-Disposition') ?? ''
  const name = /filename="?([^";]+)"?/.exec(disposition)?.[1] ?? `dismist-backup-${new Date().toISOString().replace(/[:.]/g, '-')}.db`
  const blob = await response.blob()
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = name
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 5000)
  return name
}
