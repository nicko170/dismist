import type { ReactNode } from 'react'
import { useRouterState } from '@tanstack/react-router'
import { WifiOff } from 'lucide-react'
import { useStreamStatus } from '@/api/sse'
import { BottomTabs } from './BottomTabs'
import { useNavigationEffects } from './location'
import { Sidebar } from './Sidebar'
import { useCurrentSiteId, useNavSite, useOfflineCount } from './site'
import { useDocumentTitle, usePageName } from './title'
import { TopBar } from './TopBar'

function RouteProgress() {
  const pending = useRouterState({ select: (s) => s.status === 'pending' })
  if (!pending) return null
  return (
    <div role="progressbar" aria-label="Loading" className="fixed inset-x-0 top-0 z-50 h-0.5 overflow-hidden">
      <div className="h-full w-2/5 bg-brand opacity-0 [animation:route-progress_1.1s_var(--ease-out-quart)_infinite_150ms] motion-safe:opacity-100" />
    </div>
  )
}

/** §3.0 "Stale": the stream has been down 30 s or more; data falls back to a 30 s poll. */
function StaleBanner() {
  const status = useStreamStatus()
  const inSite = Boolean(useCurrentSiteId())
  if (!inSite || status !== 'offline') return null
  return (
    <div role="status" className="flex items-center justify-center gap-2 border-b border-status-warn/40 bg-status-warn/10 px-4 py-1.5 text-xs">
      <WifiOff aria-hidden className="size-3.5" />
      Live updates paused, reconnecting…
    </div>
  )
}

function DocumentTitle() {
  const page = usePageName()
  const { site, inSite } = useNavSite()
  const offline = useOfflineCount(inSite ? site?.id : undefined)
  useDocumentTitle(page, inSite ? site?.name : undefined, offline)
  return null
}

/**
 * Phone: sticky top bar + bottom tabs. Tablet: icon rail + top bar.
 * Desktop: sidebar + top bar with breadcrumbs; content max 1280 px.
 */
export function AppShell({ children }: { children: ReactNode }) {
  useNavigationEffects()
  return (
    <div className="min-h-dvh">
      <a
        href="#main"
        className="sr-only z-[60] rounded-md bg-primary px-4 py-2 text-primary-foreground focus:not-sr-only focus:fixed focus:top-3 focus:left-3"
      >
        Skip to content
      </a>
      <DocumentTitle />
      <RouteProgress />
      <Sidebar />
      <div className="flex min-h-dvh min-w-0 flex-col md:pl-[var(--shell-rail)] lg:pl-[var(--shell-sidebar)]">
        <TopBar />
        <StaleBanner />
        <main
          id="main"
          tabIndex={-1}
          className="mx-auto w-full max-w-[80rem] min-w-0 flex-1 pt-5 pr-[max(1rem,env(safe-area-inset-right))] pb-[calc(var(--shell-tabs)+env(safe-area-inset-bottom)+1.5rem)] pl-[max(1rem,env(safe-area-inset-left))] outline-none md:px-6 md:pt-7 md:pb-12 lg:px-8"
        >
          {children}
        </main>
      </div>
      <BottomTabs />
    </div>
  )
}
