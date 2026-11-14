import { lazy, Suspense, useState } from 'react'
import { useRouterState } from '@tanstack/react-router'
import { Ellipsis } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { useKeyboardOpen } from './keyboard'
import { activeNavId, globalNav, siteNav } from './nav'
import { NavLink } from './NavLink'
import { useNavSite, useOfflineCount } from './site'

// Phone tab bar (design §2.2): Overview, APs (offline badge), Clients, WLANs,
// More. 64 px plus the safe area; hidden while the on-screen keyboard is open.

// vaul loads on first open.
const MoreSheet = lazy(() => import('./MoreSheet'))

const tabClass = cn(
  'relative flex h-full flex-col items-center justify-center gap-1 text-xs text-muted-foreground transition-colors',
  'before:absolute before:top-0 before:h-0.5 before:w-8 before:rounded-b-full before:bg-brand before:opacity-0',
  'data-[status=active]:font-medium data-[status=active]:text-foreground data-[status=active]:before:opacity-100',
)

export function BottomTabs() {
  const { siteId } = useNavSite()
  const offline = useOfflineCount(siteId)
  const keyboard = useKeyboardOpen()
  const [more, setMore] = useState(false)
  const [mounted, setMounted] = useState(false)
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const active = activeNavId(pathname)
  const moreActive = [...siteNav, ...globalNav].some((item) => item.phone === 'more' && item.id === active)
  const tabs = siteNav.filter((item) => item.phone === 'tab')

  return (
    <>
      <nav
        aria-label="Tabs"
        data-keyboard-open={keyboard || undefined}
        className={cn(
          'fixed inset-x-0 bottom-0 z-30 border-t bg-background/92 pb-safe backdrop-blur-md supports-[backdrop-filter]:bg-background/80 md:hidden',
          keyboard && 'hidden',
        )}
      >
        <ul className="grid h-[var(--shell-tabs)] grid-cols-5 pr-safe pl-safe">
          {tabs.map((item) => (
            <li key={item.id}>
              <NavLink target={item.link(siteId)} className={tabClass}>
                <span className="relative">
                  <item.icon aria-hidden className="size-[1.375rem]" />
                  {item.id === 'aps' && offline ? (
                    <Badge
                      aria-hidden
                      className="absolute -top-1.5 -right-3 h-4 min-w-4 bg-status-crit px-1 text-[0.6875rem] text-destructive-foreground ring-2 ring-background"
                    >
                      {offline}
                    </Badge>
                  ) : null}
                </span>
                <span>{item.tab ?? item.label}</span>
                {item.id === 'aps' && offline ? <span className="sr-only">, {offline} offline</span> : null}
              </NavLink>
            </li>
          ))}
          <li>
            <button
              type="button"
              className={cn(tabClass, 'w-full')}
              data-status={moreActive ? 'active' : undefined}
              aria-haspopup="dialog"
              aria-expanded={more}
              onClick={() => {
                setMounted(true)
                setMore(true)
              }}
            >
              <Ellipsis aria-hidden className="size-[1.375rem]" />
              <span>More</span>
            </button>
          </li>
        </ul>
      </nav>
      {mounted ? (
        <Suspense fallback={null}>
          <MoreSheet open={more} onOpenChange={setMore} />
        </Suspense>
      ) : null}
    </>
  )
}
