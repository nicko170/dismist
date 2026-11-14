import { Outlet, createFileRoute } from '@tanstack/react-router'
import { siteQuery } from '@/api/queries/sites'
import { SettingsLayout } from '@/features/settings/SettingsLayout'

// Site settings layout (design §3.11): H1, section tabs, legacy banner.
export const Route = createFileRoute('/sites/$siteId/settings')({
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(siteQuery(params.siteId)),
  staticData: { title: 'Settings', nav: 'settings' },
  component: function SettingsRoute() {
    const { siteId } = Route.useParams()
    return (
      <SettingsLayout siteId={siteId}>
        <Outlet />
      </SettingsLayout>
    )
  },
})
