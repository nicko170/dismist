import { createFileRoute } from '@tanstack/react-router'
import { siteAPsQuery } from '@/api/queries/aps'
import { compact, pickEnum } from '@/features/overview/kit/search'
import { OverviewPage } from '@/features/overview/OverviewPage'

// Site overview (design §3.3). The page name is the site name (§2.2), so no
// title of its own: the document title is "Home · Dismist".
export const Route = createFileRoute('/sites/$siteId/overview/')({
  validateSearch: (search: Record<string, unknown>): { range?: '1h' | '7d' } =>
    compact({ range: pickEnum(search.range, ['1h', '7d'] as const) }),
  staticData: { nav: 'overview' },
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(siteAPsQuery(params.siteId))
  },
  component: OverviewRoute,
})

function OverviewRoute() {
  const { siteId } = Route.useParams()
  const { range } = Route.useSearch()
  return <OverviewPage siteId={siteId} range={range ?? '24h'} />
}
