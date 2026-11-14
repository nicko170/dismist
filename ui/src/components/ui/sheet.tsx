import * as React from 'react'
import { Drawer } from 'vaul'
import { cn } from '@/lib/utils'

// Bottom sheets (vaul) for phones: the More menu, the site switcher and, in
// later phases, dialogs below md (design §7.7 rule 8).

export const Sheet = Drawer.Root
export const SheetTrigger = Drawer.Trigger
export const SheetClose = Drawer.Close

export function SheetContent({
  className,
  children,
  title,
  description,
  ...props
}: React.ComponentProps<typeof Drawer.Content> & { title: string; description?: string }) {
  return (
    <Drawer.Portal>
      <Drawer.Overlay className="fixed inset-0 z-50 bg-foreground/30 backdrop-blur-[2px] dark:bg-background/60" />
      <Drawer.Content
        data-slot="sheet-content"
        className={cn(
          'fixed inset-x-0 bottom-0 z-50 flex max-h-[88dvh] flex-col rounded-t-2xl border-t bg-popover pb-safe text-popover-foreground outline-none',
          className,
        )}
        {...props}
      >
        <div aria-hidden className="mx-auto mt-2.5 mb-1 h-1 w-10 shrink-0 rounded-full bg-muted-foreground/30" />
        <Drawer.Title className="px-5 pt-2 pb-1 text-base font-semibold">{title}</Drawer.Title>
        {description ? (
          <Drawer.Description className="px-5 text-sm text-muted-foreground">{description}</Drawer.Description>
        ) : (
          <Drawer.Description className="sr-only">{title}</Drawer.Description>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pt-2 pb-3">{children}</div>
      </Drawer.Content>
    </Drawer.Portal>
  )
}
