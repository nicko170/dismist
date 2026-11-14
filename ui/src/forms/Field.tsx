import { createContext, useContext, useId, type ReactNode } from 'react'
import { DirtyMarker, ExperimentalBadge, ShadowedBadge, UnverifiedBadge } from '@/components/app/badges'
import { cn } from '@/lib/utils'

// The frame every form field shares (design §7.4 "Field components"): label,
// help text, an inline error wired with aria-invalid and aria-describedby,
// experimental / shadowed / unverified markers, a dirty marker, and an
// optional slot for the inherited value and its Override switch.

export type FieldIds = {
  id: string
  labelId: string
  helpId: string
  errorId: string
  /** aria-describedby for the control. */
  describedBy: string | undefined
  invalid: boolean
}

const FieldContext = createContext<FieldIds | null>(null)

/** The ids of the enclosing field, for custom controls. */
export function useFieldIds(): FieldIds {
  const ids = useContext(FieldContext)
  if (!ids) throw new Error('useFieldIds must be used inside <FieldShell>')
  return ids
}

export type ShadowInfo = Parameters<typeof ShadowedBadge>[0]

export type FieldChrome = {
  label: ReactNode
  help?: ReactNode
  /** Visually hide the label (it still names the control). */
  hideLabel?: boolean
  required?: boolean
  experimental?: boolean
  unverified?: boolean
  shadowed?: ShadowInfo
  /** Show the "Changed" marker (from formState.dirtyFields). */
  dirty?: boolean
  /** Inherited value and Override switch (AP scope); see InheritToggle. */
  inherit?: ReactNode
  className?: string
}

export function FieldShell({
  label,
  help,
  hideLabel,
  required,
  experimental,
  unverified,
  shadowed,
  dirty,
  inherit,
  error,
  className,
  layout = 'stack',
  labelFor = true,
  children,
}: FieldChrome & {
  error?: string
  /** `inline`: label and help on the left, control on the right (switches). */
  layout?: 'stack' | 'inline'
  /** False for groups (radio, chips) that label themselves with aria-labelledby. */
  labelFor?: boolean
  /** The control; a function receives the ids to spread (see controlProps). */
  children: ReactNode | ((ids: FieldIds) => ReactNode)
}) {
  const base = useId()
  const ids: FieldIds = {
    id: `${base}-control`,
    labelId: `${base}-label`,
    helpId: `${base}-help`,
    errorId: `${base}-error`,
    describedBy: [error ? `${base}-error` : null, help ? `${base}-help` : null].filter(Boolean).join(' ') || undefined,
    invalid: Boolean(error),
  }
  const LabelTag = labelFor ? 'label' : 'span'
  const control = typeof children === 'function' ? children(ids) : children
  const labelNode = (
    <span className={cn('flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1', hideLabel && 'sr-only')}>
      <LabelTag id={`${base}-label`} {...(labelFor ? { htmlFor: ids.id } : {})} className="text-sm font-medium">
        {label}
        {required ? (
          <span aria-hidden className="ml-0.5 text-muted-foreground">
            *
          </span>
        ) : null}
      </LabelTag>
      {experimental ? <ExperimentalBadge /> : null}
      {unverified ? <UnverifiedBadge /> : null}
      {dirty ? <DirtyMarker /> : null}
    </span>
  )
  const helpNode = help ? (
    <p id={ids.helpId} className="max-w-prose text-sm text-muted-foreground">
      {help}
    </p>
  ) : null
  const errorNode = error ? (
    <p id={ids.errorId} role="alert" className="text-sm font-medium text-destructive">
      {error}
    </p>
  ) : null

  return (
    <FieldContext.Provider value={ids}>
      <div
        role={labelFor ? undefined : 'group'}
        aria-labelledby={labelFor ? undefined : `${base}-label`}
        className={cn('grid min-w-0 gap-1.5', className)}
        data-field=""
        data-invalid={error ? '' : undefined}
      >
        {layout === 'inline' ? (
          <div className="flex items-start justify-between gap-4">
            <div className="grid min-w-0 gap-1">
              {labelNode}
              {helpNode}
            </div>
            <div className="-my-1.5 shrink-0 fine:md:my-0">{control}</div>
          </div>
        ) : (
          <>
            {labelNode}
            {inherit}
            {control}
            {helpNode}
          </>
        )}
        {layout === 'inline' && inherit ? inherit : null}
        {shadowed ? <ShadowedBadge {...shadowed} /> : null}
        {errorNode}
      </div>
    </FieldContext.Provider>
  )
}

/** Spreads the field's ids onto a native control. */
export function controlProps(ids: FieldIds) {
  return {
    id: ids.id,
    'aria-invalid': ids.invalid || undefined,
    'aria-describedby': ids.describedBy,
  } as const
}
