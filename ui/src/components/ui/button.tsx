import * as React from 'react'
import { Slot } from 'radix-ui'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

// Touch first: 44 px controls below md and on any coarse pointer; compact
// sizes only with a mouse at md and up (design §7.7 rule 3).
export const buttonVariants = cva(
  'inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-[color,background-color,border-color,box-shadow] duration-150 outline-none select-none disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-4',
  {
    variants: {
      variant: {
        default: 'bg-primary text-primary-foreground hover:bg-primary/88 active:bg-primary/80',
        brand: 'bg-brand text-brand-foreground hover:bg-brand/90 active:bg-brand/85',
        outline: 'border border-input/60 bg-card text-foreground hover:bg-accent active:bg-accent/80',
        secondary: 'bg-secondary text-secondary-foreground hover:bg-accent',
        ghost: 'text-foreground hover:bg-accent active:bg-accent/80',
        destructive: 'bg-destructive text-destructive-foreground hover:bg-destructive/90',
        link: 'h-auto px-0 text-brand underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-11 px-4 fine:md:h-9 fine:md:px-3.5',
        sm: 'h-11 px-3 fine:md:h-8 fine:md:px-2.5',
        lg: 'h-12 px-5 text-base fine:md:h-10 fine:md:text-sm',
        icon: 'size-11 fine:md:size-9',
        'icon-sm': 'size-11 fine:md:size-8',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
)

export type ButtonProps = React.ComponentProps<'button'> &
  VariantProps<typeof buttonVariants> & { asChild?: boolean }

export function Button({ className, variant, size, asChild = false, type, ...props }: ButtonProps) {
  const Comp = asChild ? Slot.Root : 'button'
  return (
    <Comp
      data-slot="button"
      type={asChild ? undefined : (type ?? 'button')}
      className={cn(buttonVariants({ variant, size }), className)}
      {...props}
    />
  )
}
