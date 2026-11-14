import type { ReactNode } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// The save bar of a dirty form (design §3.0 "Saving", §7.7 rule 7). On
// phones it is fixed above the tab bar, and above the on-screen keyboard
// while one is open (the shell hides the tabs then and publishes the
// keyboard's height as --kb-inset). From md it sticks to the bottom of the
// form. It names how many fields changed and the impact of saving them.

export function StickyActionBar({
  open,
  changed,
  impact,
  onDiscard,
  onSave,
  form,
  saveLabel = 'Save',
  discardLabel = 'Discard',
  pending = false,
  disabled = false,
  className,
}: {
  /** Usually formState.isDirty. */
  open: boolean
  /** Number of changed fields ("3 changes"). */
  changed?: number
  /** The impact line (an ImpactNotice). */
  impact?: ReactNode
  onDiscard?: () => void
  /** Click handler for Save; omit and pass `form` to submit a form instead. */
  onSave?: () => void
  /** The id of the form Save submits. */
  form?: string
  saveLabel?: string
  discardLabel?: string
  pending?: boolean
  disabled?: boolean
  className?: string
}) {
  if (!open) return null
  const count = changed === undefined ? 'Unsaved changes' : changed === 1 ? '1 change' : `${changed} changes`
  return (
    <>
      {/* Keeps the end of the page reachable above the fixed bar on phones. */}
      <div aria-hidden className="h-24 md:hidden" />
      <div
        role="region"
        aria-label="Unsaved changes"
        data-action-bar=""
        className={cn(
          'action-bar-dock z-30 border-t bg-popover/95 px-[max(1rem,env(safe-area-inset-left))] py-2.5 shadow-[0_-8px_24px_-16px] shadow-foreground/20 backdrop-blur-md',
          'md:-mx-px md:mt-4 md:rounded-lg md:border md:px-4 md:shadow-lg',
          className,
        )}
      >
        <div className="mx-auto flex max-w-[80rem] flex-wrap items-center gap-x-4 gap-y-2">
          <div className="grid min-w-0 flex-1 basis-56 gap-0.5" aria-live="polite">
            <span className="text-sm font-medium">{count}</span>
            {impact ? <div className="min-w-0 text-sm text-muted-foreground">{impact}</div> : null}
          </div>
          <div className="flex shrink-0 gap-2 max-sm:w-full">
            {onDiscard ? (
              <Button variant="outline" onClick={onDiscard} disabled={pending} className="max-sm:flex-1">
                {discardLabel}
              </Button>
            ) : null}
            <Button
              variant="brand"
              type={form ? 'submit' : 'button'}
              form={form}
              onClick={onSave}
              disabled={pending || disabled}
              className="max-sm:flex-1"
            >
              {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              {pending ? 'Saving…' : saveLabel}
            </Button>
          </div>
        </div>
      </div>
    </>
  )
}
