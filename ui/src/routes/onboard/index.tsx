import { createFileRoute } from '@tanstack/react-router'
import { OnboardHub } from '@/features/onboarding/OnboardHub'
import { normaliseMac } from '@/lib/mac'

// /onboard?site=&id= (design §3.15). `id` highlights a waiting AP (the
// /aps/$apId stable link sends unassigned APs here).
export const Route = createFileRoute('/onboard/')({
  validateSearch: (search: Record<string, unknown>): { id?: string } => {
    const id = typeof search.id === 'string' ? normaliseMac(search.id) : null
    return id ? { id } : {}
  },
  component: OnboardRoute,
})

function OnboardRoute() {
  const { site, id } = Route.useSearch()
  return <OnboardHub siteId={site} highlight={id} />
}
