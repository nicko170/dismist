import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { siteAPsQuery } from '@/api/queries/aps'
import { compact, pickEnum, pickString } from '@/features/overview/kit/search'
import { APListPage, type APListSearch } from '@/features/aps/APListPage'
import { AP_FILTERS } from '@/features/overview/kit/params'

// AP list (design §3.4): ?status=&q=&sort=.
export const Route = createFileRoute('/sites/$siteId/aps/')({
  // The loader rides in the route's lazy chunk (initial JS budget, design §7.9).
  codeSplitGroupings: [['loader', 'component'], ['notFoundComponent'], ['errorComponent']],
  validateSearch: (search: Record<string, unknown>): APListSearch =>
    compact({
      status: pickEnum(search.status, AP_FILTERS),
      q: pickString(search.q, 100),
      sort: pickEnum(search.sort, ['status', 'clients', 'uptime'] as const),
    }),
  staticData: { search: true, nav: 'aps' },
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(siteAPsQuery(params.siteId))
  },
  component: APListRoute,
})

function APListRoute() {
  const { siteId } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  return <APListPage siteId={siteId} search={search} onSearch={(next) => void navigate({ search: (prev) => compact({ ...prev, ...next }), replace: true })} />
}
