import { createFileRoute } from '@tanstack/react-router'
import { apSettingsQuery } from '@/api/queries/apsettings'
import { rfQuery } from '@/api/queries/rf'
import { siteQuery } from '@/api/queries/sites'
import { APSettings } from '@/features/apconfig/APSettings'

// AP settings (design §3.6). Sections are hash anchors (#radios).
export const Route = createFileRoute('/sites/$siteId/aps/$apId/settings')({
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(apSettingsQuery(params.apId))
    void context.queryClient.prefetchQuery(siteQuery(params.siteId))
    void context.queryClient.prefetchQuery(rfQuery(params.siteId))
  },
  staticData: { title: 'AP settings', detail: true, nav: 'aps' },
  component: function APSettingsRoute() {
    const { siteId, apId } = Route.useParams()
    return <APSettings key={apId} siteId={siteId} apId={apId} />
  },
})
