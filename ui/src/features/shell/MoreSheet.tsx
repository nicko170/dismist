import { LogOut } from 'lucide-react'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import { useSignOut } from './hooks'
import { globalNav, siteNav } from './nav'
import { NavLink } from './NavLink'
import { useNavSite } from './site'
import { ThemeSegmented } from './ThemeControls'

// The More sheet (design §2.2): RF, Events, Changes, Settings, Onboard,
// System, Theme, Sign out. The one sign-out on phones lives here.

const rowClass =
  'group flex min-h-12 items-center gap-3 rounded-lg px-3 text-base hover:bg-accent data-[status=active]:bg-accent data-[status=active]:font-medium'

export default function MoreSheet({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { siteId } = useNavSite()
  const signOut = useSignOut()
  const items = [...siteNav.filter((item) => item.phone === 'more' && siteId), ...globalNav]
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent title="More">
        <nav aria-label="More">
          <ul className="grid gap-0.5">
            {items.map((item) => (
              <li key={item.id}>
                <NavLink target={item.link(siteId)} className={rowClass} onClick={() => onOpenChange(false)}>
                  <item.icon aria-hidden className="size-5 text-muted-foreground group-data-[status=active]:text-brand" />
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <div className="mt-3 grid gap-2 border-t px-1 pt-4">
          <p id="more-theme" className="px-2 text-sm text-muted-foreground">
            Theme
          </p>
          <ThemeSegmented />
        </div>
        <div className="mt-3 border-t pt-2">
          <button
            type="button"
            className="flex min-h-12 w-full items-center gap-3 rounded-lg px-3 text-base text-destructive hover:bg-destructive/8"
            onClick={() => signOut.mutate()}
            disabled={signOut.isPending}
          >
            <LogOut aria-hidden className="size-5" />
            Sign out
          </button>
        </div>
      </SheetContent>
    </Sheet>
  )
}
