import { createRouter } from '@tanstack/react-router'
import type { QueryClient } from '@tanstack/react-query'
import { ErrorState } from '@/components/app/states'
import { NotFound } from '@/components/app/NotFound'
import { PendingPage } from '@/features/shell/PendingPage'
import type { NavId } from '@/features/shell/nav'
import { routeTree } from './routeTree.gen'

/**
 * The same bundle serves /next/ (phases 0 and 1) and / (from M1), so the
 * basepath is chosen at runtime (design §2.4).
 */
export function resolveBasepath(pathname: string): '/next' | '/' {
  return pathname === '/next' || pathname.startsWith('/next/') ? '/next' : '/'
}

export function createAppRouter(queryClient: QueryClient, pathname = window.location.pathname) {
  return createRouter({
    routeTree,
    basepath: resolveBasepath(pathname),
    context: { queryClient },
    defaultPreload: 'intent',
    // TanStack Query owns freshness; loaders call ensureQueryData.
    defaultPreloadStaleTime: 0,
    scrollRestoration: true,
    defaultPendingMs: 300,
    defaultPendingMinMs: 200,
    defaultPendingComponent: PendingPage,
    defaultErrorComponent: ({ error, reset }) => (
      <ErrorState error={error} title="This page could not load" onRetry={reset} className="my-6" />
    ),
    defaultNotFoundComponent: () => <NotFound />,
  })
}

export type AppRouter = ReturnType<typeof createAppRouter>

declare module '@tanstack/react-router' {
  interface Register {
    router: AppRouter
  }
  interface StaticDataRouteOption {
    /** Page name for the title and breadcrumbs. */
    title?: string
    /** Breadcrumb label when it differs from the title. */
    crumb?: string
    /** Render without the app shell (login). */
    bare?: boolean
    /** A detail page: phones show a back chevron to the parent list. */
    detail?: boolean
    /** Show the top bar list search bound to ?q=. */
    search?: boolean
    /** Highlights this nav item when the URL alone cannot tell. */
    nav?: NavId
  }
}
