import { Check, LayoutGrid } from 'lucide-react'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import type { SiteSummary } from '@/api/types'
import { online } from './SiteSwitcher'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
  sites: SiteSummary[]
  siteId: string | undefined
  onPick: (siteId: string | null) => void
}

export default function SiteSheet({ open, onOpenChange, sites, siteId, onPick }: Props) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent title="Switch site">
        <ul className="grid gap-0.5">
          {sites.map((s) => (
            <li key={s.id}>
              <button
                type="button"
                className="flex min-h-14 w-full items-center gap-3 rounded-lg px-3 text-left hover:bg-accent aria-[current=true]:bg-accent"
                aria-current={s.id === siteId}
                onClick={() => onPick(s.id)}
              >
                <span className="grid min-w-0 flex-1">
                  <span className="truncate text-base font-medium">{s.name}</span>
                  <span className="truncate text-sm text-muted-foreground">{online(s)}</span>
                </span>
                {s.id === siteId ? <Check aria-hidden className="size-5 text-brand" /> : null}
              </button>
            </li>
          ))}
          <li className="mt-1 border-t pt-1">
            <button
              type="button"
              className="flex min-h-12 w-full items-center gap-3 rounded-lg px-3 text-left text-base hover:bg-accent"
              onClick={() => onPick(null)}
            >
              <LayoutGrid aria-hidden className="size-5 text-muted-foreground" />
              All sites
            </button>
          </li>
        </ul>
      </SheetContent>
    </Sheet>
  )
}
