import { createFileRoute, redirect } from '@tanstack/react-router'
import { sessionQuery } from '@/api/session'
import { LoginPage } from '@/features/auth/LoginPage'
import { safeNext } from '@/features/auth/next'

export const Route = createFileRoute('/login')({
  // A plain validator keeps zod out of the initial bundle (design §7.9 budget).
  validateSearch: (search: Record<string, unknown>): { next?: string } =>
    typeof search.next === 'string' ? { next: search.next } : {},
  staticData: { title: 'Sign in', bare: true },
  beforeLoad: async ({ context, search }) => {
    const session = await context.queryClient.fetchQuery(sessionQuery).catch(() => null)
    if (session?.authenticated) throw redirect({ href: safeNext(search.next), replace: true })
  },
  component: LoginRoute,
})

function LoginRoute() {
  const { next } = Route.useSearch()
  return <LoginPage next={next} />
}
