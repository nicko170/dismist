import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const alertVariants = cva(
  'relative grid w-full grid-cols-[auto_1fr] items-start gap-x-3 gap-y-0.5 rounded-lg border px-4 py-3 text-sm [&>svg]:mt-0.5 [&>svg]:size-4',
  {
    variants: {
      variant: {
        default: 'bg-card text-card-foreground [&>svg]:text-muted-foreground',
        destructive: 'border-destructive/35 bg-destructive/6 text-foreground [&>svg]:text-destructive',
        warning: 'border-status-warn/45 bg-status-warn/8 text-foreground [&>svg]:text-warning',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export function Alert({ className, variant, ...props }: React.ComponentProps<'div'> & VariantProps<typeof alertVariants>) {
  return <div data-slot="alert" role="alert" className={cn(alertVariants({ variant }), className)} {...props} />
}

export function AlertTitle({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('col-start-2 font-medium', className)} {...props} />
}

export function AlertDescription({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('col-start-2 text-muted-foreground', className)} {...props} />
}
