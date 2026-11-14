import { useId, useState, type ReactNode } from 'react'
import { AlertDialog, Dialog } from 'radix-ui'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { inputClass } from '@/components/ui/input'
import { cn } from '@/lib/utils'

// Confirmations (design P6, §3.0 "Disruptive actions"): an AlertDialog that
// names the object and the impact. Never in the URL (§2.5). Below md every
// dialog is a full-height sheet (§7.7 rule 8).

const overlay = 'fixed inset-0 z-50 bg-foreground/30 backdrop-blur-[2px] dark:bg-background/60 data-[state=open]:animate-in data-[state=open]:fade-in-0'
const panel = cn(
  'fixed z-50 flex flex-col gap-4 bg-popover text-popover-foreground outline-none',
  'max-md:inset-x-0 max-md:bottom-0 max-md:max-h-[92dvh] max-md:overflow-y-auto max-md:rounded-t-2xl max-md:border-t max-md:px-5 max-md:pt-5 max-md:pb-[max(1.25rem,env(safe-area-inset-bottom))]',
  'md:top-1/2 md:left-1/2 md:w-[min(32rem,calc(100vw-3rem))] md:-translate-x-1/2 md:-translate-y-1/2 md:rounded-xl md:border md:p-6 md:shadow-lg',
)

type ConfirmProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: ReactNode
  confirmLabel: string
  destructive?: boolean
  pending?: boolean
  error?: string | null
  /** Ask the operator to type this text before the confirm button enables. */
  typeToConfirm?: string
  /** Keeps the confirm button disabled (for example: experimental options are off). */
  confirmDisabled?: boolean
  onConfirm: () => void
  children?: ReactNode
}

export function ConfirmDialog(props: ConfirmProps) {
  const { open, onOpenChange, pending = false } = props
  return (
    <AlertDialog.Root open={open} onOpenChange={(next) => !pending && onOpenChange(next)}>
      <AlertDialog.Portal>
        <AlertDialog.Overlay className={overlay} />
        <AlertDialog.Content className={panel}>
          {/* The body unmounts when the dialog closes, so the typed confirmation resets. */}
          <ConfirmBody {...props} />
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  )
}

function ConfirmBody({
  title,
  description,
  confirmLabel,
  destructive = false,
  pending = false,
  error,
  typeToConfirm,
  confirmDisabled = false,
  onConfirm,
  children,
}: ConfirmProps) {
  const [typed, setTyped] = useState('')
  const inputId = useId()
  const blocked = Boolean(typeToConfirm) && typed.trim() !== typeToConfirm
  return (
    <>
      <AlertDialog.Title className="text-lg font-semibold">{title}</AlertDialog.Title>
      <AlertDialog.Description asChild>
        <div className="grid gap-2 text-sm text-muted-foreground">{description}</div>
      </AlertDialog.Description>
      {children}
      {typeToConfirm ? (
        <div className="grid gap-1.5">
          <label htmlFor={inputId} className="text-sm">
            Type <span className="font-mono font-medium">{typeToConfirm}</span> to confirm
          </label>
          <input
            id={inputId}
            className={inputClass}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            autoComplete="off"
          />
        </div>
      ) : null}
      {error ? (
        <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm">
          {error}
        </p>
      ) : null}
      <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <AlertDialog.Cancel asChild>
          <Button variant="outline" disabled={pending}>
            Cancel
          </Button>
        </AlertDialog.Cancel>
        <Button
          variant={destructive ? 'destructive' : 'default'}
          disabled={pending || blocked || confirmDisabled}
          onClick={(event) => {
            event.preventDefault()
            onConfirm()
          }}
        >
          {pending ? 'Working…' : confirmLabel}
        </Button>
      </div>
    </>
  )
}

/** A dialog with its own content (a sheet below md). */
export function ResponsiveDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  dismissable = true,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  children: ReactNode
  footer?: ReactNode
  dismissable?: boolean
}) {
  return (
    <Dialog.Root open={open} onOpenChange={(next) => (dismissable || next) && onOpenChange(next)}>
      <Dialog.Portal>
        <Dialog.Overlay className={overlay} />
        <Dialog.Content className={panel} aria-describedby={description ? undefined : undefined}>
          <div className="flex items-start justify-between gap-3">
            <Dialog.Title className="text-lg font-semibold">{title}</Dialog.Title>
            <Dialog.Close asChild>
              <Button variant="ghost" size="icon-sm" aria-label="Close" className="-mt-2 -mr-2">
                <X />
              </Button>
            </Dialog.Close>
          </div>
          {description ? (
            <Dialog.Description asChild>
              <div className="text-sm text-muted-foreground">{description}</div>
            </Dialog.Description>
          ) : (
            <Dialog.Description className="sr-only">{title}</Dialog.Description>
          )}
          {children}
          {footer ? <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">{footer}</div> : null}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

/** A side panel on md and up, a full-height sheet on phones (event detail). */
export function DetailDrawer({
  open,
  onOpenChange,
  title,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  children: ReactNode
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className={overlay} />
        <Dialog.Content
          className={cn(
            'fixed z-50 flex flex-col bg-popover text-popover-foreground outline-none',
            'max-md:inset-x-0 max-md:bottom-0 max-md:max-h-[92dvh] max-md:rounded-t-2xl max-md:border-t max-md:pb-[env(safe-area-inset-bottom)]',
            'md:inset-y-0 md:right-0 md:w-[28rem] md:border-l md:shadow-xl',
          )}
        >
          <div className="flex items-start justify-between gap-3 border-b px-5 py-3">
            <Dialog.Title className="min-w-0 pt-2 text-base font-semibold break-words">{title}</Dialog.Title>
            <Dialog.Close asChild>
              <Button variant="ghost" size="icon-sm" aria-label="Close">
                <X />
              </Button>
            </Dialog.Close>
          </div>
          <Dialog.Description className="sr-only">Details for {title}</Dialog.Description>
          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-4">{children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
