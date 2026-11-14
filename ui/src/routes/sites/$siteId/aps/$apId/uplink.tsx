import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { apQuery } from '@/api/queries/aps'
import { usePageTitle } from '@/features/shell/title'
import { APUplinkTab } from '@/features/aps/APUplinkTab'

// AP detail, Uplink tab (design §3.5): ports, LLDP, PoE, IP; refreshed every 10 s.
export const Route = createFileRoute('/sites/$siteId/aps/$apId/uplink')({
  staticData: { title: 'Uplink' },
  component: UplinkTab,
})

function UplinkTab() {
  const { apId } = Route.useParams()
  const ap = useQuery({ ...apQuery(apId), refetchInterval: 10_000 })
  usePageTitle(ap.data ? `${ap.data.name} uplink` : null)
  return ap.data ? <APUplinkTab ap={ap.data} /> : null
}
