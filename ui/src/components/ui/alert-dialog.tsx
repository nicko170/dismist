import * as React from 'react'
import { AlertDialog as AlertPrimitive } from 'radix-ui'
import { cn } from '@/lib/utils'

// Confirmations (design P6, §3.0): a bottom sheet on phones, a centred panel
// from md. Escape and Cancel close it; clicking outside does not.

export const AlertDialog = AlertPrimitive.Root
export const AlertDialogTrigger = AlertPrimitive.Trigger
export const AlertDialogCancel = AlertPrimitive.Cancel
export const AlertDialogAction = AlertPrimitive.Action

export function AlertDialogContent({
  className,
  children,
  ...props
}: React.ComponentProps<typeof AlertPrimitive.Content>) {
  return (
    <AlertPrimitive.Portal>
      <AlertPrimitive.Overlay className="fixed inset-0 z-50 bg-foreground/30 backdrop-blur-[2px] data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0 dark:bg-background/60" />
      <AlertPrimitive.Content
        data-slot="alert-dialog-content"
        className={cn(
          'fixed inset-x-0 bottom-0 z-50 grid gap-4 rounded-t-2xl border-t bg-popover px-5 pt-5 pb-[max(1.25rem,env(safe-area-inset-bottom))] text-popover-foreground shadow-xl shadow-foreground/10 outline-none',
          'data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:slide-in-from-bottom-6 data-[state=closed]:animate-out data-[state=closed]:fade-out-0',
          'md:inset-auto md:top-1/2 md:left-1/2 md:w-[min(28rem,calc(100vw-3rem))] md:-translate-x-1/2 md:-translate-y-1/2 md:rounded-xl md:border md:p-6 md:data-[state=open]:slide-in-from-bottom-0 md:data-[state=open]:zoom-in-95',
          className,
        )}
        {...props}
      >
        {children}
      </AlertPrimitive.Content>
    </AlertPrimitive.Portal>
  )
}

export function AlertDialogTitle({ className, ...props }: React.ComponentProps<typeof AlertPrimitive.Title>) {
  return <AlertPrimitive.Title className={cn('text-base font-semibold break-words', className)} {...props} />
}

export function AlertDialogDescription({ className, ...props }: React.ComponentProps<typeof AlertPrimitive.Description>) {
  return <AlertPrimitive.Description className={cn('text-sm text-muted-foreground', className)} {...props} />
}
