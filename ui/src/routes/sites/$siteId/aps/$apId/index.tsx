import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { apQuery } from '@/api/queries/aps'
import { APOverviewTab } from '@/features/aps/APOverviewTab'

// AP detail, Overview tab (design §3.5).
export const Route = createFileRoute('/sites/$siteId/aps/$apId/')({
  component: OverviewTab,
})

function OverviewTab() {
  const { siteId, apId } = Route.useParams()
  const ap = useQuery(apQuery(apId))
  return ap.data ? <APOverviewTab ap={ap.data} siteId={siteId} /> : null
}
