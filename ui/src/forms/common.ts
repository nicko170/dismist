import type { FieldChrome } from './Field'
import type { Inherit } from './inherit'

/** Props every bound field takes. `name` is a react-hook-form path in the enclosing <AppForm>. */
export type BoundField = Omit<FieldChrome, 'dirty' | 'inherit'> & {
  name: string
  disabled?: boolean
  /** Show the "Changed" marker while the value differs from the default (default true). */
  markDirty?: boolean
  /** AP scope: inherit from the site with an Override switch. */
  inherit?: Inherit
}

export function errorText(error: unknown): string | undefined {
  if (!error || typeof error !== 'object') return undefined
  const message = (error as { message?: unknown }).message
  if (typeof message === 'string' && message) return message
  // Arrays (ListField): the first item error.
  if (Array.isArray(error)) for (const item of error) if (item && typeof item.message === 'string') return item.message
  return undefined
}
