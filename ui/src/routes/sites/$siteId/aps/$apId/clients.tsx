import { useMemo } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { apQuery } from '@/api/queries/aps'
import { siteClientsQuery } from '@/api/queries/clients'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { usePageTitle } from '@/features/shell/title'
import { ClientList } from '@/features/clients/ClientList'
import { sortClients } from '@/features/clients/model'

// AP detail, Clients tab: the site client list filtered to this AP (design §3.5).
export const Route = createFileRoute('/sites/$siteId/aps/$apId/clients')({
  staticData: { title: 'Clients' },
  component: ClientsTab,
})

function ClientsTab() {
  const { siteId, apId } = Route.useParams()
  const ap = useQuery(apQuery(apId))
  const clients = useQuery(siteClientsQuery(siteId))
  const rows = useMemo(() => sortClients((clients.data ?? []).filter((c) => c.ap_id === apId)), [clients.data, apId])
  usePageTitle(ap.data ? `${ap.data.name} clients` : null)
  if (clients.isPending) return <SkeletonRows rows={3} />
  if (clients.isError) return <ErrorState error={clients.error} title="Could not load clients" onRetry={() => void clients.refetch()} />
  if (!rows.length) return <EmptyState title={ap.data?.connected === false ? 'The AP is offline' : 'No clients on this AP right now'} />
  return <ClientList clients={rows} siteId={siteId} showAP={false} />
}
