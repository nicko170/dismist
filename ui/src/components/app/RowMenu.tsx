import { useState, type ReactNode } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Ellipsis, type LucideIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import { useMediaQuery } from '@/features/shell/hooks'
import { cn } from '@/lib/utils'

// The trailing ⋯ menu of a row, card or detail header (design §3.0 "Lists",
// §7.7 rule 5): a dropdown with a mouse at md and up, a bottom sheet with
// 48 px rows on phones and touch screens.

export type RowAction = {
  id: string
  label: string
  icon?: LucideIcon
  onSelect?: () => void
  /** Router path instead of a handler. */
  to?: string
  search?: Record<string, unknown>
  destructive?: boolean
  disabled?: boolean
  /** Why it is disabled, shown under the label in the sheet. */
  hint?: ReactNode
}

export function RowMenu({ label, actions, className }: { label: string; actions: readonly RowAction[]; className?: string }) {
  const desktop = useMediaQuery('(min-width: 48rem) and (pointer: fine)')
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const run = (action: RowAction) => {
    setOpen(false)
    if (action.to) void navigate({ to: action.to as never, search: action.search as never })
    else action.onSelect?.()
  }
  const trigger = (
    <Button variant="ghost" size="icon-sm" aria-label={label} className={cn('text-muted-foreground', className)} onClick={desktop ? undefined : () => setOpen(true)}>
      <Ellipsis />
    </Button>
  )
  if (desktop) {
    return (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {actions.map((action) => (
            <DropdownMenuItem
              key={action.id}
              disabled={action.disabled}
              onSelect={() => run(action)}
              className={cn(action.destructive && 'text-destructive [&_svg]:text-destructive')}
            >
              {action.icon ? <action.icon aria-hidden /> : null}
              {action.label}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    )
  }
  return (
    <>
      {trigger}
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent title={label}>
          <ul className="grid gap-0.5">
            {actions.map((action) => (
              <li key={action.id}>
                <button
                  type="button"
                  disabled={action.disabled}
                  onClick={() => run(action)}
                  className={cn(
                    'flex min-h-12 w-full cursor-pointer items-center gap-3 rounded-lg px-3 text-left text-base hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50',
                    action.destructive && 'text-destructive',
                  )}
                >
                  {action.icon ? <action.icon aria-hidden className="size-5 shrink-0 text-muted-foreground" /> : null}
                  <span className="grid min-w-0 gap-0.5 py-2">
                    <span>{action.label}</span>
                    {action.hint ? <span className="text-sm text-muted-foreground">{action.hint}</span> : null}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </SheetContent>
      </Sheet>
    </>
  )
}
