import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { networksQuery } from '@/api/queries/networks'
import { WlanEditor } from '@/features/wlans/WlanEditor'

// WLAN editor, create (design §3.9). `?preset=` picks Home, Guest or IoT
// defaults; `?from=<id>` duplicates an existing WLAN.
const search = z.object({
  preset: z.enum(['home', 'guest', 'iot']).optional().catch(undefined),
  from: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/sites/$siteId/wlans/new')({
  validateSearch: (input) => search.parse(input),
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(networksQuery(params.siteId)),
  staticData: { title: 'New WLAN', detail: true, nav: 'wlans' },
  component: function NewWlanRoute() {
    const { siteId } = Route.useParams()
    const { preset, from } = Route.useSearch()
    return <WlanEditor key={`${preset}:${from}`} siteId={siteId} preset={preset} from={from} />
  },
})
