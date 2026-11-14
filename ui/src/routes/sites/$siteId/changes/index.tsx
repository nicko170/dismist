import { createFileRoute } from '@tanstack/react-router'
import { changesQuery } from '@/api/queries/changes'
import { ChangesList } from '@/features/changes/Changes'

// Config history (design §3.13 "Changes").
export const Route = createFileRoute('/sites/$siteId/changes/')({
  loader: ({ context, params }) => void context.queryClient.prefetchInfiniteQuery(changesQuery(params.siteId)),
  staticData: { title: 'Changes', nav: 'changes' },
  component: function ChangesRoute() {
    const { siteId } = Route.useParams()
    return <ChangesList siteId={siteId} />
  },
})
