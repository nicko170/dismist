import { createFileRoute } from '@tanstack/react-router'
import { RegisterPage, type RegisterSearch } from '@/features/onboarding/RegisterPage'

// /onboard/register?id=&site=&step=form|verify (design §2.3, §2.5): the step
// lives in the URL, so a refresh or a shared link keeps the Verify panel.
export const Route = createFileRoute('/onboard/register')({
  validateSearch: (search: Record<string, unknown>): Omit<RegisterSearch, 'site'> => {
    const out: Omit<RegisterSearch, 'site'> = {}
    if (typeof search.id === 'string' && search.id.trim()) out.id = search.id.trim().slice(0, 64)
    if (typeof search.name === 'string' && search.name.trim()) out.name = search.name.trim().slice(0, 64)
    if (search.step === 'verify' && out.id) out.step = 'verify'
    return out
  },
  staticData: { title: 'Register AP', detail: true },
  component: RegisterRoute,
})

function RegisterRoute() {
  const search = Route.useSearch()
  return <RegisterPage search={search} />
}
