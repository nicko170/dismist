import { createFileRoute, notFound } from '@tanstack/react-router'
import { KitPage } from '@/features/kit/KitPage'

// The component kit (mock and dev builds only): every shared component and
// form control on one page, so Playwright checks them at every width (16 px
// inputs, tap targets, axe, no sideways scroll). Production builds answer
// not found.
export const Route = createFileRoute('/dev/kit')({
  staticData: { title: 'Component kit' },
  beforeLoad: () => {
    if (import.meta.env.MODE !== 'mock' && !import.meta.env.DEV) throw notFound()
  },
  component: KitPage,
})
