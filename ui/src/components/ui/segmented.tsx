import * as React from 'react'
import { RadioGroup } from 'radix-ui'
import { cn } from '@/lib/utils'

// A segmented control is a radio group (one choice of a few, all visible).
// Segments are 44 px tall on touch and wrap rather than scroll sideways.

export function Segmented({ className, ...props }: React.ComponentProps<typeof RadioGroup.Root>) {
  return (
    <RadioGroup.Root
      data-slot="segmented"
      orientation="horizontal"
      className={cn('inline-flex max-w-full flex-wrap gap-0.5 rounded-lg border border-input/50 bg-muted p-0.5', className)}
      {...props}
    />
  )
}

export function SegmentedItem({ className, ...props }: React.ComponentProps<typeof RadioGroup.Item>) {
  return (
    <RadioGroup.Item
      data-slot="segmented-item"
      className={cn(
        'inline-flex h-11 min-w-11 flex-1 cursor-pointer items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium whitespace-nowrap text-muted-foreground transition-colors duration-150 outline-none select-none',
        'hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring',
        'data-[state=checked]:bg-card data-[state=checked]:text-foreground data-[state=checked]:shadow-xs data-[state=checked]:ring-1 data-[state=checked]:ring-foreground/8',
        'disabled:cursor-not-allowed disabled:opacity-50 fine:md:h-8 [&_svg]:size-4 [&_svg]:shrink-0',
        className,
      )}
      {...props}
    />
  )
}
