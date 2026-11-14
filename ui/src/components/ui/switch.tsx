import * as React from 'react'
import { Switch as SwitchPrimitive } from 'radix-ui'
import { cn } from '@/lib/utils'

// A 40×22 track inside a 44 px-tall hit area on touch (design §7.7 rule 3):
// the root's padding is the hit area and the background is clipped to the
// content box, so the visible pill stays small while the target does not.
export function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        'peer group inline-flex h-11 w-11 shrink-0 cursor-pointer items-center rounded-full bg-input/45 bg-clip-content px-0.5 py-[0.6875rem] transition-colors duration-150 outline-none',
        'focus-visible:outline-2 focus-visible:outline-offset-[-0.375rem] focus-visible:outline-ring',
        'data-[state=checked]:bg-brand disabled:cursor-not-allowed disabled:opacity-50',
        'fine:md:h-[1.375rem] fine:md:py-0',
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block size-[1.125rem] translate-x-0.5 rounded-full bg-card shadow-sm ring-1 ring-foreground/5 transition-transform duration-150 data-[state=checked]:translate-x-5" />
    </SwitchPrimitive.Root>
  )
}
