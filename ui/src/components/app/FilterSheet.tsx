import { useState, type ReactNode } from 'react'
import { SlidersHorizontal } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import { cn } from '@/lib/utils'

// Phone filters (design §7.7 rule 5): a "Filters" button with a count chip
// opens a bottom sheet; edits stay a draft until Apply, so the list does not
// jump under the sheet. From md the screen shows its filters inline and this
// button is hidden (pass `alwaysVisible` to keep it).

export function FilterSheet<T>({
  value,
  defaults,
  onApply,
  count,
  title = 'Filters',
  alwaysVisible = false,
  className,
  children,
}: {
  value: T
  /** What Reset returns to. */
  defaults: T
  onApply: (value: T) => void
  /** Active filters, shown on the button. */
  count: number
  title?: string
  alwaysVisible?: boolean
  className?: string
  children: (draft: T, setDraft: (next: T) => void) => ReactNode
}) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(value)
  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (next) setDraft(value)
        setOpen(next)
      }}
    >
      <Button
        variant="outline"
        className={cn(!alwaysVisible && 'md:hidden', className)}
        aria-label={count ? `${title}, ${count} active` : title}
        onClick={() => {
          setDraft(value)
          setOpen(true)
        }}
      >
        <SlidersHorizontal aria-hidden />
        {title}
        {count ? <Badge className="bg-brand text-brand-foreground">{count}</Badge> : null}
      </Button>
      <SheetContent title={title}>
        <div className="grid gap-4 px-3 pb-2">{children(draft, setDraft)}</div>
        <div className="sticky bottom-0 mt-2 flex gap-2 border-t bg-popover px-3 pt-3">
          <Button variant="outline" className="flex-1" onClick={() => setDraft(defaults)}>
            Reset
          </Button>
          <Button
            variant="brand"
            className="flex-1"
            onClick={() => {
              onApply(draft)
              setOpen(false)
            }}
          >
            Apply
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  )
}
