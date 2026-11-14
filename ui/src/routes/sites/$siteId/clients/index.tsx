import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { siteClientsQuery } from '@/api/queries/clients'
import { normaliseMac } from '@/lib/mac'
import { compact, pickEnum, pickString } from '@/features/overview/kit/search'
import { ClientsPage, type ClientsSearch } from '@/features/clients/ClientsPage'
import { BANDS, SIGNALS } from '@/features/overview/kit/params'

// Clients (design §3.12): ?q=&ap=&wlan=&band=&signal=&view=live|recent&sort=.
export const Route = createFileRoute('/sites/$siteId/clients/')({
  // The loader rides in the route's lazy chunk (initial JS budget, design §7.9).
  codeSplitGroupings: [['loader', 'component'], ['notFoundComponent'], ['errorComponent']],
  validateSearch: (search: Record<string, unknown>): ClientsSearch =>
    compact({
      q: pickString(search.q, 100),
      ap: normaliseMac(pickString(search.ap, 64) ?? '') ?? undefined,
      wlan: pickString(search.wlan, 64),
      band: pickEnum(search.band, BANDS),
      signal: pickEnum(search.signal, SIGNALS),
      view: pickEnum(search.view, ['recent'] as const),
      sort: pickEnum(search.sort, ['name', 'usage'] as const),
    }),
  staticData: { search: true },
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(siteClientsQuery(params.siteId))
  },
  component: ClientsRoute,
})

function ClientsRoute() {
  const { siteId } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  return <ClientsPage siteId={siteId} search={search} onSearch={(next) => void navigate({ search: (prev) => compact({ ...prev, ...next }), replace: true })} />
}
