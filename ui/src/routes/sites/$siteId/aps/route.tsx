import { Outlet, createFileRoute } from '@tanstack/react-router'

// Groups the AP list and AP detail so breadcrumbs read Site › Access points ›
// Hallway and the phone back chevron returns to the list.
export const Route = createFileRoute('/sites/$siteId/aps')({
  staticData: { crumb: 'Access points', title: 'Access points', nav: 'aps' },
  component: Outlet,
})
