import { createFileRoute } from '@tanstack/react-router'
import { rfQuery } from '@/api/queries/rf'
import { siteQuery } from '@/api/queries/sites'
import { RFPage } from '@/features/rf/RFPage'

// Site RF profile and channel plan (design §3.10).
export const Route = createFileRoute('/sites/$siteId/rf/')({
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(rfQuery(params.siteId))
    void context.queryClient.prefetchQuery(siteQuery(params.siteId))
  },
  staticData: { title: 'Radio settings', nav: 'rf' },
  component: function RFRoute() {
    const { siteId } = Route.useParams()
    return <RFPage siteId={siteId} />
  },
})
