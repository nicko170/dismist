import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { MutationResponse, SiteInput, SiteView } from '@/gen/api.gen'

// The site header data the monitoring screens read (design §3.3 meta line:
// country, timezone, ccrev). Site mutations belong to workstream F
// (api/queries/sites.ts); the create call lives here only because the New
// site dialog is on E's /sites route (§3.2). Same key and endpoint as F's
// siteQuery, so the two share one cache entry.

export const siteViewQuery = (siteId: string) =>
  queryOptions({
    queryKey: keys.site(siteId),
    queryFn: async ({ signal }) => {
      const body = await api.get<SiteView & { site?: SiteView }>(`/api/sites/${encodeURIComponent(siteId)}`, { signal })
      return (body?.site ?? body) as SiteView
    },
    staleTime: stale.config,
  })

/** GET /api/sites/$id/recommendations (phase 2, workstream F's data): only the count is shown here. */
export const recommendationCountQuery = (siteId: string) =>
  queryOptions({
    queryKey: [...keys.site(siteId), 'recommendations', 'count'] as const,
    queryFn: async ({ signal }) => {
      try {
        const body = await api.get<unknown[] | { recommendations?: unknown[] }>(`/api/sites/${encodeURIComponent(siteId)}/recommendations`, { signal })
        return Array.isArray(body) ? body.length : (body?.recommendations?.length ?? 0)
      } catch {
        return 0
      }
    },
    staleTime: stale.config,
  })

export function useCreateSite() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: SiteInput) => {
      const body = await api.post<SiteView | MutationResponse<SiteView>>('/api/sites', input)
      return ('object' in (body as object) ? (body as MutationResponse<SiteView>).object : body) as SiteView
    },
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.sites }),
  })
}

/** ISO 3166 alpha-2 codes with a verified regulatory table (design §3.2, [RF §5]). */
export const SUPPORTED_COUNTRIES: { code: string; name: string; ccrev: string }[] = [{ code: 'AU', name: 'Australia', ccrev: 'AU/890' }]
export const UNSUPPORTED_COUNTRIES: { code: string; name: string }[] = [
  { code: 'NZ', name: 'New Zealand' },
  { code: 'US', name: 'United States' },
  { code: 'GB', name: 'United Kingdom' },
  { code: 'CA', name: 'Canada' },
  { code: 'DE', name: 'Germany' },
  { code: 'JP', name: 'Japan' },
]

export function ccrevFor(country: string, ccrev?: string | null): string {
  if (ccrev) return ccrev.includes('/') ? ccrev : `${country}/${ccrev}`
  return SUPPORTED_COUNTRIES.find((c) => c.code === country)?.ccrev ?? ''
}

export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export function timezones(): string[] {
  try {
    const supported = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf?.('timeZone')
    if (supported?.length) return supported.includes('UTC') ? supported : ['UTC', ...supported]
  } catch {
    // Older engines: fall back to a short list.
  }
  return ['UTC', 'Australia/Sydney', 'Australia/Melbourne', 'Australia/Brisbane', 'Australia/Adelaide', 'Australia/Perth', 'Australia/Hobart', 'Australia/Darwin', 'Pacific/Auckland']
}

export function browserCountry(): string {
  try {
    const locale = new Intl.Locale(navigator.language)
    return (locale as unknown as { region?: string }).region ?? ''
  } catch {
    return ''
  }
}
