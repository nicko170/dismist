import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { PauseCircle } from 'lucide-react'
import { siteQuery, systemMigrationQuery, type ExpertSummary } from '@/api/queries/sites'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import type { ConfigFormContextValue } from './context'

// Legacy mode (design §3.0, §4.7 step 7): until the schema-2 migration has
// committed, typed forms are read-only; renames, SSIDs, passphrases,
// enable/disable, SSH keys and the expert editors still write the legacy
// fields.

export const LEGACY_EDITABLE = new Set([
  'wlan.ssid',
  'wlan.enabled',
  'wlan.psk',
  'site.name',
  'site.ssh.enabled',
  'site.ssh.keys',
  'ap.name',
])

export function useTypedEditing() {
  const { data, isError } = useQuery(systemMigrationQuery)
  const state = data?.migration?.state
  return {
    state,
    /** True once schema 2 has committed. Unknown (loading or failed) counts as not yet. */
    typed: state === 'committed',
    known: Boolean(state) || isError,
    reason: data?.migration?.reason ?? '',
  }
}

export function LegacyModeBanner() {
  const { state, known, reason } = useTypedEditing()
  if (!known || state === 'committed') return null
  return (
    <Alert variant="warning">
      <PauseCircle />
      <AlertTitle>Typed settings are paused: dismist is running your existing config unchanged.</AlertTitle>
      <AlertDescription>
        {reason ? <>{reason} </> : null}
        Names, SSIDs, passphrases, enable/disable, SSH keys and expert patches can still be edited.{' '}
        <Link to={'/system/migration' as never} className="font-medium text-brand underline-offset-2 hover:underline">
          See System › Migration
        </Link>
      </AlertDescription>
    </Alert>
  )
}

/** The form context for a site-scoped form. */
export function useSiteFormContext(
  siteId: string,
  options: { expert?: ExpertSummary | null; expertLink?: ConfigFormContextValue['expertLink'] } = {},
): ConfigFormContextValue {
  const { data: site } = useQuery(siteQuery(siteId))
  const { typed } = useTypedEditing()
  return {
    siteId,
    experimentalAllowed: Boolean(site?.experimental),
    readOnly: !typed,
    legacyEditable: typed ? null : LEGACY_EDITABLE,
    expert: options.expert ?? null,
    expertLink: options.expertLink ?? null,
  }
}
