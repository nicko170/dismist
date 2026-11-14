import * as React from 'react'
import { Dialog as DialogPrimitive } from 'radix-ui'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from './button'

// Dialogs are full-height sheets below md and centred panels from md
// (design §7.7 rule 8); `side="right"` is a side panel from md (the
// DetailDrawer). No drag-to-dismiss, so a dirty form is never lost by a
// swipe. Focus is trapped and restored by Radix.

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

const sideClass = {
  center:
    'md:inset-auto md:top-1/2 md:left-1/2 md:h-auto md:max-h-[85dvh] md:w-[min(32rem,calc(100vw-3rem))] md:-translate-x-1/2 md:-translate-y-1/2 md:rounded-xl md:border md:data-[state=open]:zoom-in-95',
  right:
    'md:inset-y-0 md:right-0 md:left-auto md:h-dvh md:w-[min(30rem,calc(100vw-5rem))] md:rounded-none md:border-l md:data-[state=open]:slide-in-from-right-8',
} as const

export function DialogContent({
  className,
  children,
  title,
  description,
  side = 'center',
  footer,
  closeLabel = 'Close',
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & {
  title: React.ReactNode
  description?: React.ReactNode
  side?: keyof typeof sideClass
  /** Sticky footer (actions); padded for the safe area on phones. */
  footer?: React.ReactNode
  closeLabel?: string
}) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-foreground/30 backdrop-blur-[2px] data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0 dark:bg-background/60" />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        className={cn(
          'fixed inset-x-0 top-[max(0.75rem,env(safe-area-inset-top))] bottom-0 z-50 flex flex-col rounded-t-2xl border-t bg-popover text-popover-foreground shadow-xl shadow-foreground/10 outline-none',
          'data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:slide-in-from-bottom-6 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 md:data-[state=open]:slide-in-from-bottom-0',
          sideClass[side],
          className,
        )}
        {...props}
      >
        <header className="flex shrink-0 items-start gap-3 border-b px-5 pt-4 pb-3 md:px-6">
          <div className="grid min-w-0 flex-1 gap-1 pt-1.5 md:pt-1">
            <DialogPrimitive.Title className="text-base font-semibold break-words">{title}</DialogPrimitive.Title>
            {description ? (
              <DialogPrimitive.Description className="text-sm text-muted-foreground">{description}</DialogPrimitive.Description>
            ) : (
              <DialogPrimitive.Description className="sr-only">{typeof title === 'string' ? title : ''}</DialogPrimitive.Description>
            )}
          </div>
          <DialogPrimitive.Close asChild>
            <Button variant="ghost" size="icon-sm" aria-label={closeLabel} className="-mr-2 text-muted-foreground">
              <X />
            </Button>
          </DialogPrimitive.Close>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-4 md:px-6">{children}</div>
        {footer ? (
          <footer className="flex shrink-0 flex-wrap justify-end gap-2 border-t px-5 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] md:px-6 md:pb-4">
            {footer}
          </footer>
        ) : null}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}
