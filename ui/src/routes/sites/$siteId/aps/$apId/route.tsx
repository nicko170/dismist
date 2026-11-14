import { Outlet, createFileRoute, notFound, redirect } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { apQuery } from '@/api/queries/aps'
import { isApiError } from '@/api/errors'
import { useStreamFallbackInterval } from '@/api/sse'
import { NotFound } from '@/components/app/NotFound'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { normaliseMac } from '@/lib/mac'
import { APHeader } from '@/features/aps/APHeader'
import { siteViewQuery } from '@/features/overview/site'
import { LinkTabs } from '@/features/overview/kit/LinkTabs'

// AP detail layout (design §3.5): the header and the tab strip for Overview,
// Radios, Clients, Uplink, Events, Settings and Config. An AP that lives in
// another site redirects there (same tab); a deleted AP is not found.
export const Route = createFileRoute('/sites/$siteId/aps/$apId')({
  // The loader rides in the route's lazy chunk (initial JS budget, design §7.9).
  codeSplitGroupings: [['loader', 'component'], ['notFoundComponent'], ['errorComponent']],
  beforeLoad: ({ params, location }) => {
    const id = normaliseMac(params.apId)
    if (id && id !== params.apId) {
      const rest = location.pathname.split(`/aps/${params.apId}`)[1] ?? ''
      throw redirect({ to: `/sites/${params.siteId}/aps/${id}${rest}` as never, replace: true })
    }
  },
  staticData: { detail: true },
  loader: async ({ context, params, location }) => {
    try {
      const ap = await context.queryClient.ensureQueryData(apQuery(params.apId))
      if (ap.site_id && ap.site_id !== params.siteId) {
        const rest = location.pathname.split(`/aps/${params.apId}`)[1] ?? ''
        throw redirect({ to: `/sites/${ap.site_id}/aps/${params.apId}${rest}` as never, replace: true })
      }
      return { crumb: ap.name }
    } catch (error) {
      if (isApiError(error) && error.status === 404) throw notFound({ data: { kind: 'ap' } })
      throw error
    }
  },
  notFoundComponent: APNotFound,
  component: APLayout,
})

function APNotFound() {
  const { siteId } = Route.useParams()
  return <NotFound title="Access point not found" detail="No AP with this ID is registered. It may have been deleted." to={`/sites/${siteId}/aps`} action="All access points" />
}

function APLayout() {
  const { siteId, apId } = Route.useParams()
  const fallback = useStreamFallbackInterval()
  const ap = useQuery({ ...apQuery(apId), refetchInterval: fallback })
  const site = useQuery({ ...siteViewQuery(siteId), retry: false })
  if (ap.isPending) return <SkeletonRows rows={4} />
  if (ap.isError) return <ErrorState error={ap.error} title="Could not load this AP" onRetry={() => void ap.refetch()} />
  const params = { siteId, apId }
  return (
    <div className="grid min-w-0 gap-5">
      <APHeader ap={ap.data} siteId={siteId} experimental={Boolean(site.data?.experimental)} />
      <LinkTabs
        label="Access point sections"
        items={[
          { label: 'Overview', to: '/sites/$siteId/aps/$apId', params, exact: true },
          { label: 'Radios', to: '/sites/$siteId/aps/$apId/radios', params },
          { label: 'Clients', to: '/sites/$siteId/aps/$apId/clients', params },
          { label: 'Uplink', to: '/sites/$siteId/aps/$apId/uplink', params },
          { label: 'Events', to: '/sites/$siteId/aps/$apId/events', params },
          { label: 'Settings', to: '/sites/$siteId/aps/$apId/settings', params },
          { label: 'Config', to: '/sites/$siteId/aps/$apId/config', params },
        ]}
      />
      <Outlet />
    </div>
  )
}
