import { createFileRoute } from '@tanstack/react-router'
import { networkQuery } from '@/api/queries/networks'
import { WlanEditor } from '@/features/wlans/WlanEditor'
import { oneOf } from '@/features/settings/kit/search'

// WLAN editor, edit (design §3.9). Sections are hash anchors (#security).
const search = (input: Record<string, unknown>) => ({ dialog: oneOf(input.dialog, ['qr'] as const) })

export const Route = createFileRoute('/sites/$siteId/wlans/$wlanId/')({
  validateSearch: search,
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(networkQuery(params.siteId, params.wlanId)),
  staticData: { title: 'WLAN', detail: true, nav: 'wlans' },
  component: function WlanRoute() {
    const { siteId, wlanId } = Route.useParams()
    const { dialog } = Route.useSearch()
    return <WlanEditor key={wlanId} siteId={siteId} wlanId={wlanId} dialog={dialog} />
  },
})
