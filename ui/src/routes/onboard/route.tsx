import { Outlet, createFileRoute } from '@tanstack/react-router'

// /onboard layout (design §2.3): names the section for titles, breadcrumbs
// and the Register page's back chevron, and carries ?site= to both pages.
export const Route = createFileRoute('/onboard')({
  validateSearch: (search: Record<string, unknown>): { site?: string } =>
    typeof search.site === 'string' && search.site ? { site: search.site } : {},
  staticData: { title: 'Add an AP', nav: 'onboard' },
  component: Outlet,
})
