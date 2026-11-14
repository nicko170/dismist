import { createFileRoute, redirect } from '@tanstack/react-router'
import { clientQuery } from '@/api/queries/clients'
import { normaliseMac } from '@/lib/mac'
import { ClientDetailPage } from '@/features/clients/ClientDetailPage'

// Client detail (design §3.12). MACs in URLs are lower-case dashed; colon or
// upper-case input redirects (§2.3).
export const Route = createFileRoute('/sites/$siteId/clients/$mac')({
  beforeLoad: ({ params }) => {
    const mac = normaliseMac(params.mac)
    if (mac && mac !== params.mac) throw redirect({ to: '/sites/$siteId/clients/$mac', params: { siteId: params.siteId, mac }, replace: true })
  },
  staticData: { detail: true },
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(clientQuery(params.mac, params.siteId))
    return { crumb: params.mac }
  },
  component: ClientRoute,
})

function ClientRoute() {
  const { siteId, mac } = Route.useParams()
  return <ClientDetailPage siteId={siteId} mac={mac} />
}
