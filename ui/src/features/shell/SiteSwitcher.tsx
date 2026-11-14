import { lazy, Suspense, useState } from 'react'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { ChevronDown, ChevronsUpDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import type { SiteSummary } from '@/api/types'
import { cn } from '@/lib/utils'
import { useNavSite } from './site'

/** Where switching to `siteId` lands: the same top-level section, else the overview. */
export function switchTarget(pathname: string, siteId: string): string {
  const section = pathname.match(/^\/sites\/[^/]+\/([^/?#]+)/)?.[1] ?? 'overview'
  return `/sites/${encodeURIComponent(siteId)}/${section}`
}

// cmdk and vaul load on first open, keeping them out of the initial bundle.
const SiteCommand = lazy(() => import('./SiteCommand'))
const SiteSheet = lazy(() => import('./SiteSheet'))

export function online(site: SiteSummary) {
  return site.counts.aps ? `${site.counts.aps_online}/${site.counts.aps} APs online` : 'No APs yet'
}

export function initials(name: string) {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (!words.length) return '–'
  return (words.length > 1 ? words[0][0] + words[1][0] : words[0][0]).toUpperCase()
}

function useSwitch() {
  const navigate = useNavigate()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  return (siteId: string | null) =>
    navigate({ to: (siteId ? switchTarget(pathname, siteId) : '/sites') as never })
}

/** Sidebar (lg) and icon rail (md) variant: a combobox with search. */
export function SiteSwitcher({ className }: { className?: string }) {
  const { site, sites, siteId } = useNavSite()
  const [open, setOpen] = useState(false)
  const go = useSwitch()
  const name = site?.name ?? 'Choose a site'
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          className={cn(
            'h-11 w-full justify-start gap-2.5 border border-sidebar-border bg-card px-2 text-left hover:bg-sidebar-accent max-lg:size-11 max-lg:justify-center max-lg:border-0 max-lg:bg-transparent max-lg:px-0',
            className,
          )}
        >
          <span aria-hidden className="grid size-7 shrink-0 place-items-center rounded-md bg-brand-soft text-xs font-semibold text-brand">
            {site ? initials(site.name) : '–'}
          </span>
          <span className="grid min-w-0 flex-1 max-lg:sr-only">
            <span className="truncate text-sm font-medium">
              <span className="sr-only">Site: </span>
              {name}
            </span>
            {site ? <span className="truncate text-xs text-muted-foreground">{online(site)}</span> : null}
            <span className="sr-only">. Switch site</span>
          </span>
          <ChevronsUpDown aria-hidden className="text-muted-foreground max-lg:hidden" />
        </Button>
      </PopoverTrigger>
      <PopoverContent side="bottom" align="start" className="w-72 p-0">
        {open ? (
          <Suspense fallback={<div className="h-40" aria-busy="true" />}>
            <SiteCommand sites={sites} siteId={siteId} onPick={(id) => {
              setOpen(false)
              void go(id)
            }} />
          </Suspense>
        ) : null}
      </PopoverContent>
    </Popover>
  )
}

/** Phone variant: the top bar button and a bottom sheet. */
export function SiteSwitcherSheet({ className }: { className?: string }) {
  const { site, sites, siteId } = useNavSite()
  const [open, setOpen] = useState(false)
  const go = useSwitch()
  const name = site?.name ?? 'Sites'
  const [mounted, setMounted] = useState(false)
  return (
    <>
      <Button
        variant="ghost"
        className={cn('h-11 max-w-[11rem] min-w-0 gap-1 px-2 font-semibold', className)}
        aria-label={`Site: ${name}. Switch site`}
        aria-haspopup="dialog"
        onClick={() => {
          setMounted(true)
          setOpen(true)
        }}
      >
        <span className="truncate">{name}</span>
        <ChevronDown aria-hidden className="text-muted-foreground" />
      </Button>
      {mounted ? (
        <Suspense fallback={null}>
          <SiteSheet open={open} onOpenChange={setOpen} sites={sites} siteId={siteId} onPick={(id) => {
            setOpen(false)
            void go(id)
          }} />
        </Suspense>
      ) : null}
    </>
  )
}
