import { useId, type ReactNode } from 'react'
import { useController } from 'react-hook-form'
import { InheritedValue } from '@/components/app/badges'
import { Switch } from '@/components/ui/switch'

// AP-scope values that inherit from the site (design §3.6): the form value
// is null while inheriting. Turning Override on copies the site value into
// the field as the starting point; turning it off resets to null.

export type Inherit = {
  /** The site's value, copied in when Override is turned on. */
  value: unknown
  /** How the site value reads ("255", "On"); defaults to String(value). */
  display?: ReactNode
  /** "Site default" unless the parent is something else ("Profile"). */
  label?: string
}

export function useInherit(name: string, inherit: Inherit | undefined, disabled = false) {
  const { field } = useController({ name })
  const switchId = useId()
  const overridden = !inherit || (field.value !== null && field.value !== undefined)
  const slot = inherit ? (
    <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
      <InheritedValue label={inherit.label} value={inherit.display ?? String(inherit.value)} />
      <span className="flex items-center gap-2 text-sm">
        <label htmlFor={switchId}>Override</label>
        <Switch
          id={switchId}
          checked={overridden}
          disabled={disabled}
          onCheckedChange={(on) => field.onChange(on ? (inherit.value ?? null) : null)}
        />
      </span>
    </div>
  ) : null
  return { overridden, slot, locked: Boolean(inherit) && !overridden }
}
