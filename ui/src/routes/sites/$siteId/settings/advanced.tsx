import { createFileRoute } from '@tanstack/react-router'
import { AdvancedSettings, SiteSettingsSection } from '@/features/settings/SiteSettings'

// Site settings › Advanced (design §3.11).
export const Route = createFileRoute('/sites/$siteId/settings/advanced')({
  staticData: { title: 'Advanced', nav: 'settings' },
  component: function SettingsPageRoute() {
    const { siteId } = Route.useParams()
    return <SiteSettingsSection siteId={siteId}>{(site) => <AdvancedSettings key={site.id} siteId={siteId} site={site} />}</SiteSettingsSection>
  },
})
