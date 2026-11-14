import { createFileRoute } from '@tanstack/react-router'
import { GeneralSettings, SiteSettingsSection } from '@/features/settings/SiteSettings'

// Site settings › General (design §3.11).
export const Route = createFileRoute('/sites/$siteId/settings/general')({
  staticData: { title: 'General', nav: 'settings' },
  component: function SettingsPageRoute() {
    const { siteId } = Route.useParams()
    return <SiteSettingsSection siteId={siteId}>{(site) => <GeneralSettings key={site.id} siteId={siteId} site={site} />}</SiteSettingsSection>
  },
})
