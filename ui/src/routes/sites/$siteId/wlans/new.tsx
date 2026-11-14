import { createFileRoute } from '@tanstack/react-router'
import { networksQuery } from '@/api/queries/networks'
import { WlanEditor } from '@/features/wlans/WlanEditor'
import { oneOf, text } from '@/features/settings/kit/search'

// WLAN editor, create (design §3.9). `?preset=` picks Home, Guest or IoT
// defaults; `?from=<id>` duplicates an existing WLAN.
const search = (input: Record<string, unknown>) => ({ preset: oneOf(input.preset, ['home', 'guest', 'iot'] as const), from: text(input.from) })

export const Route = createFileRoute('/sites/$siteId/wlans/new')({
  validateSearch: search,
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(networksQuery(params.siteId)),
  staticData: { title: 'New WLAN', detail: true, nav: 'wlans' },
  component: function NewWlanRoute() {
    const { siteId } = Route.useParams()
    const { preset, from } = Route.useSearch()
    return <WlanEditor key={`${preset}:${from}`} siteId={siteId} preset={preset} from={from} />
  },
})
