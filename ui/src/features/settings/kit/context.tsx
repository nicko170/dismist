import { createContext, useContext, type ReactNode } from 'react'
import type { CrossScopeShadow, ExpertSummary } from '@/api/queries/sites'

// What every field of a config form needs to know about its surroundings:
// whether experimental options are allowed on the site (§4.2), whether the
// form is read-only (legacy mode, §3.0), and which options an expert patch
// shadows (§3.0 "Shadowed fields").

export type ShadowState = { full: boolean; paths: string[]; crossScope: CrossScopeShadow | null }

export type ConfigFormContextValue = {
  siteId: string
  experimentalAllowed: boolean
  readOnly: boolean
  /** In legacy mode, the option keys that still write the legacy fields (§4.7 step 7). */
  legacyEditable: ReadonlySet<string> | null
  expert: ExpertSummary | null
  /** Where this scope's expert patch is edited. */
  expertLink: { to: string; search?: Record<string, string> } | null
}

const ConfigFormContext = createContext<ConfigFormContextValue>({
  siteId: '',
  experimentalAllowed: false,
  readOnly: false,
  legacyEditable: null,
  expert: null,
  expertLink: null,
})

export function ConfigFormProvider({ value, children }: { value: ConfigFormContextValue; children: ReactNode }) {
  return <ConfigFormContext.Provider value={value}>{children}</ConfigFormContext.Provider>
}

export function useConfigFormContext() {
  return useContext(ConfigFormContext)
}

export function shadowFor(expert: ExpertSummary | null, key: string): ShadowState | null {
  if (!expert) return null
  const full = expert.shadowed?.includes(key) ?? false
  const partial = expert.partly_shadowed?.find((s) => s.key === key)
  const crossScope = expert.cross_scope?.find((s) => s.key === key) ?? null
  if (!full && !partial && !crossScope) return null
  return { full, paths: partial?.paths ?? [], crossScope }
}

/** Whether a field may be edited: not read-only, or still writable in legacy mode. */
export function isFieldEditable(ctx: ConfigFormContextValue, key: string) {
  if (!ctx.readOnly) return true
  return ctx.legacyEditable?.has(key) ?? false
}
