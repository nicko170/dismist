import { Link } from '@tanstack/react-router'
import { LogOut } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import { BrandMark, Wordmark } from './brand'
import { useMediaQuery, useSignOut, useVersion } from './hooks'
import { globalNav, siteNav, type NavItem } from './nav'
import { NavLink } from './NavLink'
import { useNavSite, useOfflineCount } from './site'
import { SiteSwitcher } from './SiteSwitcher'
import { ThemeMenu } from './ThemeControls'

// Desktop sidebar (lg, 248 px) that collapses to a 64 px icon rail with
// tooltips at md (design §2.2). One component, so both share one nav model.

const itemClass = cn(
  'group relative flex h-11 items-center gap-3 rounded-md px-2.5 text-sm text-muted-foreground transition-colors',
  'hover:bg-sidebar-accent hover:text-foreground',
  'data-[status=active]:bg-sidebar-accent data-[status=active]:font-medium data-[status=active]:text-foreground',
  'before:absolute before:inset-y-2 before:-left-2.5 before:w-[3px] before:rounded-r-full before:bg-brand before:opacity-0 data-[status=active]:before:opacity-100 lg:before:-left-3',
  'max-lg:justify-center max-lg:px-0 fine:lg:h-9',
)

function RailTooltip({ label, children, enabled }: { label: string; children: React.ReactElement; enabled: boolean }) {
  if (!enabled) return children
  return (
    <Tooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent side="right">{label}</TooltipContent>
    </Tooltip>
  )
}

function Item({ item, siteId, badge, rail }: { item: NavItem; siteId: string | undefined; badge?: number; rail: boolean }) {
  return (
    <li>
      <RailTooltip label={badge ? `${item.label} (${badge} offline)` : item.label} enabled={rail}>
        <NavLink target={item.link(siteId)} className={itemClass}>
          <item.icon aria-hidden className="size-[1.125rem] shrink-0 group-data-[status=active]:text-brand" />
          <span className="truncate max-lg:sr-only">{item.label}</span>
          {badge ? (
            <Badge
              className="ml-auto bg-status-crit text-destructive-foreground max-lg:absolute max-lg:top-1 max-lg:right-1 max-lg:h-4 max-lg:min-w-4 max-lg:px-1 max-lg:text-[0.625rem]"
            >
              {badge}
              <span className="sr-only"> offline</span>
            </Badge>
          ) : null}
        </NavLink>
      </RailTooltip>
    </li>
  )
}

export function Sidebar() {
  const { siteId } = useNavSite()
  const offline = useOfflineCount(siteId)
  const rail = !useMediaQuery('(min-width: 64rem)')
  const signOut = useSignOut()
  const version = useVersion()

  return (
    <aside className="fixed inset-y-0 left-0 z-30 hidden w-[var(--shell-rail)] flex-col border-r border-sidebar-border bg-sidebar pl-safe md:flex lg:w-[var(--shell-sidebar)]">
      <div className="flex h-14 shrink-0 items-center gap-2.5 px-4 max-lg:justify-center max-lg:px-0">
        <Link to="/" aria-label="Dismist home" className="rounded-lg">
          <BrandMark />
        </Link>
        <Wordmark className="max-lg:hidden" />
      </div>

      <div className="px-3 pb-2 max-lg:flex max-lg:justify-center max-lg:px-2.5">
        <SiteSwitcher />
      </div>

      <nav aria-label="Primary" className="flex min-h-0 flex-1 flex-col overflow-y-auto px-3 py-2 max-lg:px-2.5">
        {siteId ? (
          <ul className="grid gap-0.5">
            {siteNav.map((item) => (
              <Item key={item.id} item={item} siteId={siteId} rail={rail} badge={item.id === 'aps' ? offline : undefined} />
            ))}
          </ul>
        ) : null}
        <ul className={cn('grid gap-0.5', siteId && 'mt-auto border-t border-sidebar-border pt-3')}>
          {globalNav.map((item) => (
            <Item key={item.id} item={item} siteId={siteId} rail={rail} />
          ))}
        </ul>
      </nav>

      <div className="flex shrink-0 flex-wrap items-center gap-1 border-t border-sidebar-border px-3 py-2.5 max-lg:flex-col max-lg:px-2.5">
        <ThemeMenu showLabel={!rail} />
        <RailTooltip label="Sign out" enabled={rail}>
          <Button
            variant="ghost"
            size={rail ? 'icon-sm' : 'sm'}
            className="text-muted-foreground hover:text-foreground"
            onClick={() => signOut.mutate()}
            disabled={signOut.isPending}
            aria-label={rail ? 'Sign out' : undefined}
          >
            <LogOut />
            {rail ? null : 'Sign out'}
          </Button>
        </RailTooltip>
        {version && !rail ? (
          <p className="w-full truncate px-2.5 pt-1 text-xs text-muted-foreground tabular-nums">dismist {version}</p>
        ) : null}
      </div>
    </aside>
  )
}
