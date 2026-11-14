import { createFileRoute } from '@tanstack/react-router'
import { networksQuery } from '@/api/queries/networks'
import { siteQuery } from '@/api/queries/sites'
import { WlanList } from '@/features/wlans/WlanList'
import { oneOf, text } from '@/features/settings/kit/search'

// WLAN list (design §3.8). `?dialog=qr&wlan=` opens the Wi-Fi QR dialog, so
// Back closes it (§2.5).
const search = (input: Record<string, unknown>) => ({ dialog: oneOf(input.dialog, ['qr'] as const), wlan: text(input.wlan) })

export const Route = createFileRoute('/sites/$siteId/wlans/')({
  validateSearch: search,
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
