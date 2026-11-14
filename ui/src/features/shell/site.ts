import { useParams } from '@tanstack/react-router'
import { skipToken, useQuery } from '@tanstack/react-query'
import { keys } from '@/api/keys'
import type { APView } from '@/api/types'
import { sitesQuery } from './queries'

// The current site comes from the URL; the last-used one from localStorage
// (`dismist.lastSite`, design §2.4) so site-scoped navigation still works on
// /system or /onboard.

export const LAST_SITE_KEY = 'dismist.lastSite'

export function readLastSite(): string | null {
  try {
    return localStorage.getItem(LAST_SITE_KEY)
  } catch {
    return null
  }
}

export function rememberLastSite(siteId: string) {
  try {
    localStorage.setItem(LAST_SITE_KEY, siteId)
  } catch {
    // Storage unavailable: `/` falls back to the site list.
  }
}

export function forgetLastSite(siteId?: string) {
  try {
    if (!siteId || localStorage.getItem(LAST_SITE_KEY) === siteId) localStorage.removeItem(LAST_SITE_KEY)
  } catch {
    // Nothing stored.
  }
}

export function useCurrentSiteId(): string | undefined {
  return useParams({ strict: false, select: (params) => (params as { siteId?: string }).siteId })
}

/** The site the navigation points at: the URL's, else the last used one that still exists. */
export function useNavSite() {
  const current = useCurrentSiteId()
  const { data: sites } = useQuery(sitesQuery)
  const last = readLastSite()
  const id = current ?? (last && sites?.some((s) => s.id === last) ? last : sites?.length === 1 ? sites[0].id : undefined)
  const site = sites?.find((s) => s.id === id)
  return { siteId: id, site, sites: sites ?? [], inSite: Boolean(current) }
}

/**
 * Offline APs in a site: from the live AP list when a screen has loaded it
 * (the stream keeps it current), else from the site summary counts.
 */
export function useOfflineCount(siteId: string | undefined): number {
  const { data: sites } = useQuery(sitesQuery)
  const { data: aps } = useQuery<APView[]>({
    queryKey: siteId ? keys.siteAPs(siteId) : ['site', '', 'aps'],
    queryFn: skipToken,
  })
  if (!siteId) return 0
  if (aps) return aps.filter((ap) => !ap.connected).length
  const counts = sites?.find((s) => s.id === siteId)?.counts
  return counts ? Math.max(0, counts.aps - counts.aps_online) : 0
}
