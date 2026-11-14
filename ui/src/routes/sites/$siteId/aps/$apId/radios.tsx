import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { apQuery } from '@/api/queries/aps'
import { usePageTitle } from '@/features/shell/title'
import { APRadiosTab } from '@/features/aps/APRadiosTab'

// AP detail, Radios tab (design §3.5); refreshed every 10 s while visible (§7.5).
export const Route = createFileRoute('/sites/$siteId/aps/$apId/radios')({
  staticData: { title: 'Radios' },
  component: RadiosTab,
})

function RadiosTab() {
  const { siteId, apId } = Route.useParams()
  const ap = useQuery({ ...apQuery(apId), refetchInterval: 10_000 })
  usePageTitle(ap.data ? `${ap.data.name} radios` : null)
  return ap.data ? <APRadiosTab ap={ap.data} siteId={siteId} /> : null
}
