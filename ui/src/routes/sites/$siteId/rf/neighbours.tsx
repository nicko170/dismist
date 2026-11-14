import { createFileRoute } from '@tanstack/react-router'
import { NeighboursPage } from '@/features/rf/Neighbours'
import { oneOf } from '@/features/settings/kit/search'

// Neighbour scan (design §3.10 "Neighbours"); `?band=` filters.
const search = (input: Record<string, unknown>) => ({ band: oneOf(input.band, ['2.4', '5'] as const) })

export const Route = createFileRoute('/sites/$siteId/rf/neighbours')({
  validateSearch: search,
  staticData: { title: 'Neighbours', detail: true, nav: 'rf' },
  component: function NeighboursRoute() {
    const { siteId } = Route.useParams()
    const { band } = Route.useSearch()
    return <NeighboursPage siteId={siteId} band={band} />
  },
})
