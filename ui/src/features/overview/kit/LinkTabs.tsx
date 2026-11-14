import { useEffect, useRef } from 'react'
import { Link } from '@tanstack/react-router'
import { cn } from '@/lib/utils'

// Sub-navigation as link tabs (design §2.2): on phones the strip scrolls in
// its own container with scroll-snap and an edge fade, and the active tab is
// scrolled into view; the page itself never scrolls sideways.

export type TabItem = { label: string; to: string; params?: Record<string, string>; exact?: boolean }

export function LinkTabs({ items, label, className }: { items: TabItem[]; label: string; className?: string }) {
  const strip = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const active = strip.current?.querySelector<HTMLElement>('[aria-current="page"]')
    active?.scrollIntoView?.({ block: 'nearest', inline: 'center' })
  })
  return (
    <nav aria-label={label} className={cn('relative min-w-0 border-b', className)}>
      <div
        ref={strip}
        className="flex snap-x snap-mandatory gap-1 overflow-x-auto [scrollbar-width:none] max-md:[mask-image:linear-gradient(to_right,black_85%,transparent)] [&::-webkit-scrollbar]:hidden"
      >
        {items.map((item) => (
          <Link
            key={item.to}
            to={item.to as never}
            params={item.params as never}
            activeOptions={{ exact: item.exact ?? false, includeSearch: false }}
            className={cn(
              'relative flex h-11 shrink-0 snap-start items-center px-3 text-sm whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground',
              'after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:bg-brand after:opacity-0',
              'data-[status=active]:font-medium data-[status=active]:text-foreground data-[status=active]:after:opacity-100',
            )}
          >
            {item.label}
          </Link>
        ))}
      </div>
    </nav>
  )
}
