import * as React from 'react'
import { cn } from '@/lib/utils'

// 16 px text on phones and every coarse pointer; 14 px only at md+ with a
// mouse (`text-base fine:md:text-sm`, design §7.7 rule 1).
export const inputClass =
  'flex h-11 w-full min-w-0 rounded-md border border-input/70 bg-card px-3 text-base text-foreground transition-[border-color,box-shadow] duration-150 outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/25 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/15 fine:md:h-9 fine:md:text-sm'

export function Input({ className, type = 'text', ...props }: React.ComponentProps<'input'>) {
  return <input type={type} data-slot="input" className={cn(inputClass, className)} {...props} />
}

export function Textarea({ className, ...props }: React.ComponentProps<'textarea'>) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(inputClass, 'h-auto min-h-24 py-2.5 leading-relaxed', className)}
      {...props}
    />
  )
}
