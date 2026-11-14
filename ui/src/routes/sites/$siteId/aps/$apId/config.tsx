import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { apConfigQuery } from '@/api/queries/apsettings'
import { APConfig } from '@/features/apconfig/APConfig'

// AP config (design §3.7): ?view=effective|expert|history.
const search = z.object({ view: z.enum(['effective', 'expert', 'history']).optional().catch(undefined) })

export const Route = createFileRoute('/sites/$siteId/aps/$apId/config')({
  validateSearch: (input) => search.parse(input),
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(apConfigQuery(params.apId, 'effective')),
  staticData: { title: 'AP config', detail: true, nav: 'aps' },
  component: function APConfigRoute() {
    const { siteId, apId } = Route.useParams()
    const { view } = Route.useSearch()
    return <APConfig key={apId} siteId={siteId} apId={apId} view={view ?? 'effective'} />
  },
})
