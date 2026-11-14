import { createFileRoute } from '@tanstack/react-router'
import { ProtectionSettings, SiteSettingsSection } from '@/features/settings/SiteSettings'

// Site settings › Protection (design §3.11).
export const Route = createFileRoute('/sites/$siteId/settings/protection')({
  staticData: { title: 'Protection', nav: 'settings' },
  component: function SettingsPageRoute() {
    const { siteId } = Route.useParams()
    return <SiteSettingsSection siteId={siteId}>{(site) => <ProtectionSettings key={site.id} siteId={siteId} site={site} />}</SiteSettingsSection>
  },
})
