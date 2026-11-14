import { createFileRoute } from '@tanstack/react-router'
import { NetworkSettings, SiteSettingsSection } from '@/features/settings/SiteSettings'

// Site settings › Network (design §3.11).
export const Route = createFileRoute('/sites/$siteId/settings/network')({
  staticData: { title: 'Network', nav: 'settings' },
  component: function SettingsPageRoute() {
    const { siteId } = Route.useParams()
    return <SiteSettingsSection siteId={siteId}>{(site) => <NetworkSettings key={site.id} siteId={siteId} site={site} />}</SiteSettingsSection>
  },
})
