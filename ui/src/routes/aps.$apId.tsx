import { createFileRoute, notFound, redirect } from '@tanstack/react-router'
import { apQuery } from '@/api/queries/aps'
import { isApiError } from '@/api/errors'
import { NotFound } from '@/components/app/NotFound'
import { normaliseMac } from '@/lib/mac'

// Stable AP link (design §2.3): /aps/$apId redirects to the AP in its current
// site, or to onboarding when it is not assigned to a site yet.
export const Route = createFileRoute('/aps/$apId')({
  // The loader rides in the route's lazy chunk (initial JS budget, design §7.9).
  codeSplitGroupings: [['loader', 'component'], ['notFoundComponent'], ['errorComponent']],
  loader: async ({ context, params }) => {
    const id = normaliseMac(params.apId) ?? params.apId
    try {
      const ap = await context.queryClient.ensureQueryData(apQuery(id))
      if (ap.site_id) throw redirect({ to: '/sites/$siteId/aps/$apId', params: { siteId: ap.site_id, apId: id }, replace: true })
    } catch (error) {
      if (!(isApiError(error) && error.status === 404)) throw error
      throw notFound()
    }
    throw redirect({ to: '/onboard' as never, search: { id } as never, replace: true })
  },
  notFoundComponent: () => <NotFound title="Access point not found" detail="No AP with this ID is registered." to="/sites" action="All sites" />,
})
