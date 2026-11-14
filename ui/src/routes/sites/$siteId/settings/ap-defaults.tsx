import { createFileRoute } from '@tanstack/react-router'
import { APDefaultsSettings, SiteSettingsSection } from '@/features/settings/SiteSettings'

// Site settings › AP defaults (design §3.11).
export const Route = createFileRoute('/sites/$siteId/settings/ap-defaults')({
  staticData: { title: 'AP defaults', nav: 'settings' },
  component: function SettingsPageRoute() {
    const { siteId } = Route.useParams()
    return <SiteSettingsSection siteId={siteId}>{(site) => <APDefaultsSettings key={site.id} siteId={siteId} site={site} />}</SiteSettingsSection>
  },
})
