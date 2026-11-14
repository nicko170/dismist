import { createFileRoute, notFound } from '@tanstack/react-router'
import { changeQuery } from '@/api/queries/changes'
import { ChangeDetailPage } from '@/features/changes/Changes'

// One revision: redacted diff and Restore (design §3.13, §4.11).
export const Route = createFileRoute('/sites/$siteId/changes/$rev')({
  params: {
    parse: (raw) => {
      const rev = Number(raw.rev)
      if (!Number.isInteger(rev) || rev < 0) throw notFound()
      return { rev }
    },
    stringify: (params) => ({ rev: String(params.rev) }),
  },
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(changeQuery(params.siteId, params.rev)),
  staticData: { title: 'Change', detail: true, nav: 'changes' },
  component: function ChangeRoute() {
    const { siteId, rev } = Route.useParams()
    return <ChangeDetailPage key={rev} siteId={siteId} revision={rev} />
  },
})
