import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Outlet, createFileRoute, notFound, redirect } from '@tanstack/react-router'
import { pendingCountQuery, siteAPsQuery } from '@/api/live'
import { useSiteStream, useStreamFallbackInterval } from '@/api/sse'
import { NotFound, SiteNotFound } from '@/components/app/NotFound'
import { sitesQuery } from '@/features/shell/queries'
import { forgetLastSite, rememberLastSite } from '@/features/shell/site'

// The site layout (design §2.3): loads the site, opens the site stream,
// remembers the last-used site, and sends /sites/$siteId to its overview.
// It also keeps the site's AP list subscribed: the stream's `snapshot` and
// `ap` events patch that list in place, so every screen below (and the
// shell's offline badge and title) has live AP state without refetching.
export const Route = createFileRoute('/sites/$siteId')({
  beforeLoad: ({ params, location }) => {
    const root = `/sites/${encodeURIComponent(params.siteId)}`
    if (location.pathname === root || location.pathname === `${root}/`) {
      throw redirect({ to: `${root}/overview` as never, replace: true })
    }
  },
  loader: async ({ context, params }) => {
    const sites = await context.queryClient.ensureQueryData(sitesQuery)
    const site = sites.find((candidate) => candidate.id === params.siteId)
    if (!site) {
      forgetLastSite(params.siteId)
      throw notFound({ data: { kind: 'site' } })
    }
    // Started, not awaited: screens render while the list loads.
    void context.queryClient.prefetchQuery(siteAPsQuery(params.siteId))
    return { site, crumb: site.name }
  },
  component: SiteLayout,
  notFoundComponent: ({ data }) =>
    (data as { kind?: string } | undefined)?.kind === 'site' ? <SiteNotFound /> : <NotFound />,
})

function SiteLayout() {
  const { siteId } = Route.useParams()
  useSiteStream(siteId)
  // Polls every 30 s only while the stream is offline (design §7.6).
  const fallback = useStreamFallbackInterval()
  useQuery({ ...siteAPsQuery(siteId), refetchInterval: fallback })
  useQuery({ ...pendingCountQuery, refetchInterval: fallback })
  // Remember the site only when it is actually shown (loaders also run on hover preload).
  useEffect(() => rememberLastSite(siteId), [siteId])
  return <Outlet />
}
