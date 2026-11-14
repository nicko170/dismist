import { createFileRoute } from '@tanstack/react-router'
import { apConfigQuery } from '@/api/queries/apsettings'
import { APConfig } from '@/features/apconfig/APConfig'
import { oneOf } from '@/features/settings/kit/search'

// AP config (design §3.7): ?view=effective|expert|history.
const search = (input: Record<string, unknown>) => ({ view: oneOf(input.view, ['effective', 'expert', 'history'] as const) })

export const Route = createFileRoute('/sites/$siteId/aps/$apId/config')({
  validateSearch: search,
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(apConfigQuery(params.apId, 'effective')),
  staticData: { title: 'AP config', detail: true, nav: 'aps' },
  component: function APConfigRoute() {
    const { siteId, apId } = Route.useParams()
    const { view } = Route.useSearch()
    return <APConfig key={apId} siteId={siteId} apId={apId} view={view ?? 'effective'} />
  },
})
