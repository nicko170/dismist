import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { networksQuery } from '@/api/queries/networks'
import { siteQuery } from '@/api/queries/sites'
import { WlanList } from '@/features/wlans/WlanList'

// WLAN list (design §3.8). `?dialog=qr&wlan=` opens the Wi-Fi QR dialog, so
// Back closes it (§2.5).
const search = z.object({
  dialog: z.enum(['qr']).optional().catch(undefined),
  wlan: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/sites/$siteId/wlans/')({
  validateSearch: (input) => search.parse(input),
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(networksQuery(params.siteId))
    void context.queryClient.prefetchQuery(siteQuery(params.siteId))
  },
  staticData: { title: 'WLANs', nav: 'wlans' },
  component: function WlansRoute() {
    const { siteId } = Route.useParams()
    const { dialog, wlan } = Route.useSearch()
    return <WlanList siteId={siteId} dialog={dialog} wlan={wlan} />
  },
})
