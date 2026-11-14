import { useEffect } from 'react'
import { Outlet, createFileRoute, notFound, redirect } from '@tanstack/react-router'
import { useSiteStream } from '@/api/sse'
import { NotFound, SiteNotFound } from '@/components/app/NotFound'
import { sitesQuery } from '@/features/shell/queries'
import { forgetLastSite, rememberLastSite } from '@/features/shell/site'

// The site layout (design §2.3): loads the site, opens the site stream,
// remembers the last-used site, and sends /sites/$siteId to its overview.
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
    return { site, crumb: site.name }
  },
  component: SiteLayout,
  notFoundComponent: ({ data }) =>
    (data as { kind?: string } | undefined)?.kind === 'site' ? <SiteNotFound /> : <NotFound />,
})

function SiteLayout() {
  const { siteId } = Route.useParams()
  useSiteStream(siteId)
  // Remember the site only when it is actually shown (loaders also run on hover preload).
  useEffect(() => rememberLastSite(siteId), [siteId])
  return <Outlet />
}
