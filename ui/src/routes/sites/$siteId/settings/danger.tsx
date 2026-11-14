import { createFileRoute } from '@tanstack/react-router'
import { DangerSettings, SiteSettingsSection } from '@/features/settings/SiteSettings'

// Site settings › Danger (design §3.11).
export const Route = createFileRoute('/sites/$siteId/settings/danger')({
  staticData: { title: 'Danger', nav: 'settings' },
  component: function SettingsPageRoute() {
    const { siteId } = Route.useParams()
    return <SiteSettingsSection siteId={siteId}>{(site) => <DangerSettings key={site.id} siteId={siteId} site={site} />}</SiteSettingsSection>
  },
})
