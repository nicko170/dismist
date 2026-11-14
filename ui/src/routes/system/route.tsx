import { Outlet, createFileRoute } from '@tanstack/react-router'
import { PageHeader } from '@/components/app/PageHeader'
import { LinkTabs } from '@/features/overview/kit/LinkTabs'

// System (design §3.14): controller, AP endpoint, database, backups, API
// tokens, sessions and the schema 2 migration, as link tabs.
export const Route = createFileRoute('/system')({
  staticData: { crumb: 'System', nav: 'system' },
  component: SystemLayout,
})

function SystemLayout() {
  return (
    <div className="grid min-w-0 gap-5">
      <PageHeader title="System" />
      <LinkTabs
        label="System sections"
        items={[
          { label: 'Overview', to: '/system', exact: true },
          { label: 'Backups', to: '/system/backups' },
          { label: 'API tokens', to: '/system/tokens' },
          { label: 'Sessions', to: '/system/sessions' },
          { label: 'Migration', to: '/system/migration' },
        ]}
      />
      <Outlet />
    </div>
  )
}
