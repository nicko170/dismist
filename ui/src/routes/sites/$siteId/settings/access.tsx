import { createFileRoute } from '@tanstack/react-router'
import { AccessSettings, SiteSettingsSection } from '@/features/settings/SiteSettings'

// Site settings › Access (design §3.11).
export const Route = createFileRoute('/sites/$siteId/settings/access')({
  staticData: { title: 'Access', nav: 'settings' },
  component: function SettingsPageRoute() {
    const { siteId } = Route.useParams()
    return <SiteSettingsSection siteId={siteId}>{(site) => <AccessSettings key={site.id} siteId={siteId} site={site} />}</SiteSettingsSection>
  },
})
