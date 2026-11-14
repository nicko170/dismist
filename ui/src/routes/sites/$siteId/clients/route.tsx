import { Outlet, createFileRoute } from '@tanstack/react-router'

// Groups the client list and client detail (breadcrumbs, phone back chevron).
export const Route = createFileRoute('/sites/$siteId/clients')({
  staticData: { crumb: 'Clients', title: 'Clients', nav: 'clients' },
  component: Outlet,
})
