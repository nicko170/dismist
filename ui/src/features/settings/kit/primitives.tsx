import * as React from 'react'
import { AlertDialog as AD, Dialog as DialogPrimitive, RadioGroup, Slider as SliderPrimitive, Switch as SwitchPrimitive } from 'radix-ui'
import { ChevronDown, X } from 'lucide-react'
import { buttonVariants } from '@/components/ui/button'
import { inputClass } from '@/components/ui/input'
import { cn } from '@/lib/utils'

// Primitives the configuration forms need that the shared kit (WS-D,
// components/ui) does not have yet. Built on radix-ui like the rest; offered
// to WS-D for promotion into components/ui.

/** 44 px tall touch target around a 24 px track; 20 px at md+ with a mouse. */
export function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        'peer relative inline-flex h-7 w-12 shrink-0 cursor-pointer items-center rounded-full border border-transparent bg-input/45 transition-colors duration-150 outline-none before:absolute before:-inset-2 before:content-[""] focus-visible:ring-3 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-brand fine:md:h-5 fine:md:w-9',
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block size-6 rounded-full bg-card shadow-sm ring-0 transition-transform duration-150 data-[state=checked]:translate-x-5 data-[state=unchecked]:translate-x-0 fine:md:size-4 fine:md:data-[state=checked]:translate-x-4" />
    </SwitchPrimitive.Root>
  )
}

/** A native select: the platform picker on phones, 16 px text on touch (§7.7 rule 1). */
export function NativeSelect({ className, children, ...props }: React.ComponentProps<'select'>) {
  return (
    <div className="relative min-w-0">
      <select data-slot="select" className={cn(inputClass, 'appearance-none pr-9', className)} {...props}>
        {children}
      </select>
      <ChevronDown aria-hidden className="pointer-events-none absolute top-1/2 right-3 size-4 -translate-y-1/2 text-muted-foreground" />
    </div>
  )
}

export type SegmentOption = { value: string; label: React.ReactNode; disabled?: boolean; ariaLabel?: string }

/** A segmented control: a radio group, so arrow keys move between segments. */
export function Segmented({
  value,
  onValueChange,
  options,
  disabled,
  className,
  ...props
}: {
  value: string
  onValueChange: (value: string) => void
  options: SegmentOption[]
  disabled?: boolean
  className?: string
} & Omit<React.ComponentProps<typeof RadioGroup.Root>, 'value' | 'onValueChange' | 'orientation'>) {
  return (
    <RadioGroup.Root
      value={value}
      onValueChange={onValueChange}
      disabled={disabled}
      orientation="horizontal"
      className={cn('inline-flex max-w-full flex-wrap gap-1 rounded-lg border bg-muted p-1', className)}
      {...props}
    >
      {options.map((option) => (
        <RadioGroup.Item
          key={option.value}
          value={option.value}
          disabled={option.disabled}
          aria-label={option.ariaLabel}
          className="inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center rounded-md px-3 text-sm font-medium text-muted-foreground transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-card data-[state=checked]:text-foreground data-[state=checked]:shadow-sm fine:md:min-h-8"
        >
          {option.label}
        </RadioGroup.Item>
      ))}
    </RadioGroup.Root>
  )
}

export function Slider({ className, ...props }: React.ComponentProps<typeof SliderPrimitive.Root>) {
  return (
    <SliderPrimitive.Root
      className={cn('relative flex h-11 w-full touch-none items-center select-none data-[disabled]:opacity-50 fine:md:h-6', className)}
      {...props}
    >
      <SliderPrimitive.Track className="relative h-1.5 grow overflow-hidden rounded-full bg-muted">
        <SliderPrimitive.Range className="absolute h-full bg-brand" />
      </SliderPrimitive.Track>
      <SliderPrimitive.Thumb
        aria-label={props['aria-label']}
        className="block size-6 rounded-full border-2 border-brand bg-card shadow transition-shadow outline-none focus-visible:ring-4 focus-visible:ring-ring/30 fine:md:size-4"
      />
    </SliderPrimitive.Root>
  )
}

// ---- dialogs ----

/** Centred dialog at md+, a full-height sheet below md (§7.7 rule 8). */
export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  className,
  wide,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description?: React.ReactNode
  children?: React.ReactNode
  footer?: React.ReactNode
  className?: string
  wide?: boolean
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-foreground/30 backdrop-blur-[2px] data-[state=open]:animate-in data-[state=open]:fade-in-0 dark:bg-background/60" />
        <DialogPrimitive.Content
          className={cn(
            'fixed inset-x-0 bottom-0 z-50 flex max-h-[100dvh] min-h-[60dvh] flex-col rounded-t-2xl border-t bg-popover pb-safe text-popover-foreground shadow-xl outline-none md:inset-auto md:top-1/2 md:left-1/2 md:max-h-[85dvh] md:min-h-0 md:w-[min(36rem,calc(100vw-3rem))] md:-translate-x-1/2 md:-translate-y-1/2 md:rounded-xl md:border',
            wide && 'md:w-[min(56rem,calc(100vw-3rem))]',
            className,
          )}
        >
          <div className="flex items-start gap-3 border-b px-5 pt-4 pb-3">
            <div className="grid min-w-0 flex-1 gap-1">
              <DialogPrimitive.Title className="text-base font-semibold">{title}</DialogPrimitive.Title>
              {description ? (
                <DialogPrimitive.Description className="text-sm text-muted-foreground">{description}</DialogPrimitive.Description>
              ) : (
                <DialogPrimitive.Description className="sr-only">{typeof title === 'string' ? title : 'Dialog'}</DialogPrimitive.Description>
              )}
            </div>
            <DialogPrimitive.Close className={buttonVariants({ variant: 'ghost', size: 'icon-sm', className: '-mt-1 -mr-2' })} aria-label="Close">
              <X />
            </DialogPrimitive.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-4">{children}</div>
          {footer ? <div className="flex flex-wrap justify-end gap-2 border-t px-5 py-3">{footer}</div> : null}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

/**
 * AlertDialog for disruptive actions (§3.0): names the object and the impact.
 * `confirmText`, when set, must be typed to enable the action (delete site).
 */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  action,
  cancel = 'Cancel',
  destructive,
  pending,
  onConfirm,
  confirmText,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description?: React.ReactNode
  children?: React.ReactNode
  action: string
  cancel?: string
  destructive?: boolean
  pending?: boolean
  onConfirm: () => void
  confirmText?: string
}) {
  const [typed, setTyped] = React.useState('')
  const inputId = React.useId()
  React.useEffect(() => {
    if (!open) setTyped('')
  }, [open])
  const blocked = Boolean(confirmText) && typed.trim() !== confirmText
  return (
    <AD.Root open={open} onOpenChange={onOpenChange}>
      <AD.Portal>
        <AD.Overlay className="fixed inset-0 z-50 bg-foreground/30 backdrop-blur-[2px] dark:bg-background/60" />
        <AD.Content className="fixed inset-x-0 bottom-0 z-50 grid max-h-[100dvh] gap-4 overflow-y-auto rounded-t-2xl border-t bg-popover px-5 pt-5 pb-[max(1.25rem,env(safe-area-inset-bottom))] text-popover-foreground shadow-xl outline-none md:inset-auto md:top-1/2 md:left-1/2 md:w-[min(30rem,calc(100vw-3rem))] md:-translate-x-1/2 md:-translate-y-1/2 md:rounded-xl md:border md:pb-5">
          <div className="grid gap-1.5">
            <AD.Title className="text-base font-semibold">{title}</AD.Title>
            <AD.Description className={cn('text-sm text-muted-foreground', !description && 'sr-only')}>
              {description ?? (typeof title === 'string' ? title : 'Confirm')}
            </AD.Description>
          </div>
          {children}
          {confirmText ? (
            <div className="grid gap-1.5">
              <label htmlFor={inputId} className="text-sm">
                Type <span className="font-mono font-medium">{confirmText}</span> to confirm
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
          <div className="flex flex-col-reverse gap-2 md:flex-row md:justify-end">
            <AD.Cancel className={buttonVariants({ variant: 'outline' })}>{cancel}</AD.Cancel>
            <button
              type="button"
              className={buttonVariants({ variant: destructive ? 'destructive' : 'default' })}
              disabled={pending || blocked}
              onClick={onConfirm}
            >
              {pending ? 'Working…' : action}
            </button>
          </div>
        </AD.Content>
      </AD.Portal>
    </AD.Root>
  )
}
