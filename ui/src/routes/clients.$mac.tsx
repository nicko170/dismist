import { createFileRoute, notFound, redirect } from '@tanstack/react-router'
import { api } from '@/api/client'
import { isApiError } from '@/api/errors'
import { NotFound } from '@/components/app/NotFound'
import { normaliseMac } from '@/lib/mac'

// Stable client link (design §2.3): GET /api/clients/$mac without a site
// answers where the client was last seen; redirect there.
export const Route = createFileRoute('/clients/$mac')({
  // The loader rides in the route's lazy chunk (initial JS budget, design §7.9).
  codeSplitGroupings: [['loader', 'component'], ['notFoundComponent'], ['errorComponent']],
  loader: async ({ params }) => {
    const mac = normaliseMac(params.mac)
    if (!mac) throw notFound()
    let siteId = ''
    try {
      const body = await api.get<{ site_id?: string }>(`/api/clients/${encodeURIComponent(mac)}`)
      siteId = body?.site_id ?? ''
    } catch (error) {
      if (isApiError(error) && error.status === 404) throw notFound()
      throw error
    }
    if (!siteId) throw notFound()
    throw redirect({ to: '/sites/$siteId/clients/$mac', params: { siteId, mac }, replace: true })
  },
  notFoundComponent: () => (
    <NotFound title="Client not found" detail="dismist has not seen this client recently. It may have been away longer than the history keeps." to="/sites" action="All sites" />
  ),
})
