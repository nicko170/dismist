import { Link, useMatches, useNavigate, useRouter, useSearch } from '@tanstack/react-router'
import { ChevronLeft, ChevronRight, Ellipsis } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { SearchInput } from '@/components/app/SearchInput'
import { StreamIndicator } from '@/components/app/StreamIndicator'
import { cn } from '@/lib/utils'
import { getPreviousPathname } from './location'
import { useNavSite } from './site'
import { SiteSwitcherSheet } from './SiteSwitcher'
import { themeOptions } from './ThemeControls'
import { useTheme, type ThemeChoice } from './theme'
import { useCrumbs, usePageName } from './title'

function Breadcrumbs() {
  const crumbs = useCrumbs()
  if (!crumbs.length) return null
  return (
    <nav aria-label="Breadcrumb" className="min-w-0">
      <ol className="flex min-w-0 items-center gap-1.5 text-sm">
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1
          return (
            <li key={crumb.to} className={cn('flex min-w-0 items-center gap-1.5', !last && 'shrink-[2]')}>
              {index > 0 ? <ChevronRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground/70" /> : null}
              {last ? (
                <span aria-current="page" className="truncate font-medium">
                  {crumb.label}
                </span>
              ) : (
                <Link to={crumb.to as never} className="truncate text-muted-foreground hover:text-foreground">
                  {crumb.label}
                </Link>
              )}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}

/** List search bound to ?q=, shown on routes with staticData.search; `/` focuses it. */
function ListSearch({ className }: { className?: string }) {
  const navigate = useNavigate()
  const q = useSearch({ strict: false, select: (s) => (s as { q?: string }).q ?? '' })
  return (
    <SearchInput
      className={className}
      label="Search this list"
      value={q}
      shortcut
      onChange={(next) =>
        void navigate({
          to: '.' as never,
          search: ((prev: Record<string, unknown>) => ({ ...prev, q: next || undefined })) as never,
          replace: true,
        })
      }
    />
  )
}

function BackButton({ to }: { to: string }) {
  const router = useRouter()
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label="Back"
      onClick={() => {
        if (getPreviousPathname() === to) router.history.back()
        else void router.navigate({ to: to as never })
      }}
    >
      <ChevronLeft className="size-5" />
    </Button>
  )
}

function OverflowMenu() {
  const { theme, setTheme } = useTheme()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="More options">
          <Ellipsis className="size-5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(value) => setTheme(value as ThemeChoice)}>
          {themeOptions.map((option) => (
            <DropdownMenuRadioItem key={option.value} value={option.value}>
              <option.icon />
              {option.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function TopBar() {
  const page = usePageName()
  const { site } = useNavSite()
  const detail = useMatches({
    select: (matches) => {
      const index = matches.findLastIndex((m) => m.staticData?.detail)
      if (index < 0) return null
      // The parent list: the closest earlier match that names a page.
      const parent = matches.slice(0, index).findLast((m) => m.staticData?.title || m.staticData?.crumb)
      return parent?.pathname ?? matches[index - 1]?.pathname ?? '/'
    },
  })
  const search = useMatches({ select: (matches) => matches.some((m) => m.staticData?.search) })

  return (
    <header className="sticky top-0 z-20 border-b bg-background/88 pt-safe backdrop-blur-md supports-[backdrop-filter]:bg-background/72">
      {/* Phones: site switcher (or back), page title, stream dot, overflow. */}
      <div className="flex h-14 items-center gap-1 pr-[max(0.25rem,env(safe-area-inset-right))] pl-[max(0.25rem,env(safe-area-inset-left))] md:hidden">
        {detail ? <BackButton to={detail} /> : <SiteSwitcherSheet />}
        <p className="min-w-0 flex-1 truncate px-1 text-center text-sm text-muted-foreground" aria-hidden>
          {page !== site?.name ? page : null}
        </p>
        <StreamIndicator compact />
        <OverflowMenu />
      </div>
      {/* Tablet and desktop: breadcrumbs, list search, stream indicator. */}
      <div className="hidden h-14 items-center gap-4 px-6 md:flex lg:px-8">
        <div className="min-w-0 flex-1">
          <Breadcrumbs />
        </div>
        {search ? <ListSearch className="w-56 lg:w-72" /> : null}
        <StreamIndicator />
      </div>
    </header>
  )
}
