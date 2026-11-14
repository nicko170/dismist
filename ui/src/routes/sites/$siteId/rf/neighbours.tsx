import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { NeighboursPage } from '@/features/rf/Neighbours'

// Neighbour scan (design §3.10 "Neighbours"); `?band=` filters.
const search = z.object({ band: z.enum(['2.4', '5']).optional().catch(undefined) })

export const Route = createFileRoute('/sites/$siteId/rf/neighbours')({
  validateSearch: (input) => search.parse(input),
  staticData: { title: 'Neighbours', detail: true, nav: 'rf' },
  component: function NeighboursRoute() {
    const { siteId } = Route.useParams()
    const { band } = Route.useSearch()
    return <NeighboursPage siteId={siteId} band={band} />
  },
})
