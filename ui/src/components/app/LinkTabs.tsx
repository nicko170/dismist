import { useEffect, useRef } from 'react'
import { Link, useRouterState } from '@tanstack/react-router'
import { cn } from '@/lib/utils'

// Sub-navigation as link tabs (design §2.2: the AP detail tabs; settings
// sections). On phones the strip scrolls sideways inside itself with snap
// points and edge fades, and the active tab scrolls into view; the page
// never scrolls sideways.

export type LinkTab = { to: string; label: string; search?: Record<string, unknown>; badge?: number | string }

/** Exact match for the first (index) tab, prefix match for the rest. */
export function tabIsActive(pathname: string, to: string, index: boolean): boolean {
  const target = to.replace(/\/+$/, '')
  const path = pathname.replace(/\/+$/, '')
  return index ? path === target : path === target || path.startsWith(`${target}/`)
}

export function LinkTabs({ tabs, label, className }: { tabs: readonly LinkTab[]; label: string; className?: string }) {
  const strip = useRef<HTMLUListElement>(null)
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const activeIndex = tabs.findIndex((tab, index) => tabIsActive(pathname, tab.to, index === 0 && tabs.length > 1 && tabs.slice(1).some((t) => t.to.startsWith(`${tab.to}/`))))

  useEffect(() => {
    const active = activeIndex >= 0 ? strip.current?.children[activeIndex]?.firstElementChild : null
    active?.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'instant' as ScrollBehavior })
  }, [activeIndex])

  return (
    <nav aria-label={label} className={cn('relative min-w-0 border-b', className)}>
      <ul
        ref={strip}
        className="flex snap-x snap-mandatory scroll-px-4 overflow-x-auto overscroll-x-contain [scrollbar-width:none] [mask-image:linear-gradient(to_right,transparent,black_1rem,black_calc(100%-1.5rem),transparent)] md:[mask-image:none] [&::-webkit-scrollbar]:hidden"
      >
        {tabs.map((tab, index) => {
          const active = index === activeIndex
          return (
            <li key={tab.to} className="shrink-0 snap-start first:pl-1 last:pr-4">
              <Link
                to={tab.to as never}
                search={tab.search as never}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'relative flex h-11 items-center gap-1.5 px-3 text-sm whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground',
                  'after:absolute after:inset-x-2 after:bottom-[-1px] after:h-0.5 after:rounded-full after:bg-brand after:opacity-0',
                  active && 'font-medium text-foreground after:opacity-100',
                )}
              >
                {tab.label}
                {tab.badge !== undefined && tab.badge !== 0 ? (
                  <span className="rounded-full bg-muted px-1.5 text-xs text-muted-foreground tabular-nums">{tab.badge}</span>
                ) : null}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
