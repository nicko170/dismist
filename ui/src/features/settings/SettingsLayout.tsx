import { useEffect, useRef, type ReactNode } from 'react'
import { Link, useRouterState } from '@tanstack/react-router'
import { PageHeader } from '@/components/app/PageHeader'
import { cn } from '@/lib/utils'
import { UnsavedChangesGuard } from './kit/guard'
import { LegacyModeBanner } from './kit/site'

// Site settings frame (design §2.3, §3.11): the H1, link tabs to the
// sub-pages (a scrolling strip on phones with the active tab in view), the
// legacy-mode banner and the unsaved-changes guard.

export const SETTINGS_PAGES = [
  { slug: 'general', label: 'General' },
  { slug: 'access', label: 'Access' },
  { slug: 'network', label: 'Network' },
  { slug: 'ap-defaults', label: 'AP defaults' },
  { slug: 'protection', label: 'Protection' },
  { slug: 'advanced', label: 'Advanced' },
  { slug: 'danger', label: 'Danger' },
] as const

export type SettingsSlug = (typeof SETTINGS_PAGES)[number]['slug']

export function SettingsLayout({ siteId, children }: { siteId: string; children: ReactNode }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const active = SETTINGS_PAGES.find((p) => pathname.endsWith(`/settings/${p.slug}`))?.slug
  const strip = useRef<HTMLElement>(null)
  useEffect(() => {
    if (!active) return
    strip.current?.querySelector<HTMLElement>(`[data-slug="${active}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [active])
  return (
    <div className="grid gap-5">
      <PageHeader title="Settings" />
      <nav ref={strip} aria-label="Settings sections" className="-mx-4 overflow-x-auto px-4 [scrollbar-width:none] md:mx-0 md:px-0">
        <ul className="flex w-max snap-x gap-1 border-b md:w-auto md:flex-wrap">
          {SETTINGS_PAGES.map((page) => (
            <li key={page.slug} className="snap-start">
              <Link
                to={`/sites/${encodeURIComponent(siteId)}/settings/${page.slug}` as never}
                aria-current={active === page.slug ? 'page' : undefined}
                data-slug={page.slug}
                className={cn(
                  '-mb-px inline-flex min-h-11 items-center border-b-2 border-transparent px-3 text-sm font-medium whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground fine:md:min-h-9',
                  active === page.slug && 'border-brand text-foreground',
                  page.slug === 'danger' && 'text-destructive',
                )}
              >
                {page.label}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      <LegacyModeBanner />
      {children}
      <UnsavedChangesGuard />
    </div>
  )
}
