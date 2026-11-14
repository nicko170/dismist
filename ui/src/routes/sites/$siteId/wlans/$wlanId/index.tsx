import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { networkQuery } from '@/api/queries/networks'
import { WlanEditor } from '@/features/wlans/WlanEditor'

// WLAN editor, edit (design §3.9). Sections are hash anchors (#security).
const search = z.object({ dialog: z.enum(['qr']).optional().catch(undefined) })

export const Route = createFileRoute('/sites/$siteId/wlans/$wlanId/')({
  validateSearch: (input) => search.parse(input),
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(networkQuery(params.siteId, params.wlanId)),
  staticData: { title: 'WLAN', detail: true, nav: 'wlans' },
  component: function WlanRoute() {
    const { siteId, wlanId } = Route.useParams()
    const { dialog } = Route.useSearch()
    return <WlanEditor key={wlanId} siteId={siteId} wlanId={wlanId} dialog={dialog} />
  },
})
