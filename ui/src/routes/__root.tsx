import { Outlet, createRootRouteWithContext, redirect, useMatches } from '@tanstack/react-router'
import type { QueryClient } from '@tanstack/react-query'
import { lazy, Suspense } from 'react'
import { ErrorState } from '@/components/app/states'
import { NotFound, SiteNotFound } from '@/components/app/NotFound'
import { sessionQuery } from '@/api/session'
import { AppShell } from '@/features/shell/AppShell'

export type RouterContext = { queryClient: QueryClient }

const Devtools = import.meta.env.DEV
  ? lazy(() => import('@tanstack/react-query-devtools').then((m) => ({ default: m.ReactQueryDevtools })))
  : () => null

// Every screen except /login needs a session (design §2.4). The session is
// cached for 60 s; any 401 from the API redirects through the client handler.
export const Route = createRootRouteWithContext<RouterContext>()({
  beforeLoad: async ({ context, location }) => {
    if (location.pathname === '/login') return
    const session = await context.queryClient.fetchQuery(sessionQuery)
    if (!session.authenticated) {
      throw redirect({ to: '/login', search: { next: location.href }, replace: true })
    }
  },
  component: Root,
  notFoundComponent: RootNotFound,
  errorComponent: ({ error, reset }) => (
    <main id="main" className="mx-auto grid min-h-dvh max-w-lg content-center px-4">
      <ErrorState error={error} title="dismist could not start this page" onRetry={reset} />
    </main>
  ),
})

function Root() {
  const bare = useMatches({ select: (matches) => matches.some((m) => m.staticData?.bare) })
  return (
    <>
      {bare ? (
        <Outlet />
      ) : (
        <AppShell>
          <Outlet />
        </AppShell>
      )}
      <Suspense fallback={null}>
        <Devtools buttonPosition="top-right" />
      </Suspense>
    </>
  )
}

function RootNotFound({ data }: { data?: unknown }) {
  if ((data as { kind?: string } | undefined)?.kind === 'site') return <SiteNotFound />
  return <NotFound />
}
