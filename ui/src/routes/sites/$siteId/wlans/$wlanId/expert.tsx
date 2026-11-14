import { createFileRoute } from '@tanstack/react-router'
import { networkExpertQuery } from '@/api/queries/networks'
import { WlanExpert } from '@/features/wlans/WlanExpert'

// WLAN expert patch (design §3.9 "Expert").
export const Route = createFileRoute('/sites/$siteId/wlans/$wlanId/expert')({
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(networkExpertQuery(params.siteId, params.wlanId)),
  staticData: { title: 'Expert patch', detail: true, nav: 'wlans' },
  component: function WlanExpertRoute() {
    const { siteId, wlanId } = Route.useParams()
    return <WlanExpert siteId={siteId} wlanId={wlanId} />
  },
})
