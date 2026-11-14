import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { FlaskConical, Layers, ShieldQuestion } from 'lucide-react'
import { cn } from '@/lib/utils'

// Markers next to a field, a section or a list row (design §3.0, P4).
// Every marker is text, not only an icon, so it reads without hover.

const badgeBase = 'inline-flex h-6 max-w-full items-center gap-1 rounded-md border px-1.5 text-xs font-medium whitespace-nowrap'

export const EXPERIMENTAL_HELP = 'Not yet verified on AP33 0.15. Test on a lab AP first.'

/** A needs-live-test option or value (design P4). */
export function ExperimentalBadge({ className, label = 'Experimental' }: { className?: string; label?: string }) {
  return (
    <span className={cn(badgeBase, 'border-brand/35 bg-brand-soft text-foreground', className)} title={EXPERIMENTAL_HELP}>
      <FlaskConical aria-hidden className="size-3.5 text-brand" />
      {label}
    </span>
  )
}

/** A value that is not yet measured or verified (units, client impact; design §9 Q4, Q7). */
export function UnverifiedBadge({ className, label = 'Unverified' }: { className?: string; label?: string }) {
  return (
    <span className={cn(badgeBase, 'border-dashed border-input text-muted-foreground', className)}>
      <ShieldQuestion aria-hidden className="size-3.5" />
      {label}
    </span>
  )
}

export type ShadowLink = { to: string; label: string }

/**
 * A typed field an expert patch overrides (design §3.0 "Shadowed fields").
 * `full`: every firmware path is overridden (the field is disabled);
 * `paths`: only these are; `elsewhere`: overridden on other objects (an AP
 * patch over a site field, or an AP's typed override).
 */
export function ShadowedBadge({
  full = false,
  paths = [],
  elsewhere,
  patch,
  className,
}: {
  full?: boolean
  paths?: readonly string[]
  elsewhere?: { kind: 'patch' | 'override'; links: readonly ShadowLink[] }
  /** Link to the expert patch that does it. */
  patch?: ShadowLink
  className?: string
}) {
  let text: ReactNode
  if (elsewhere) {
    const n = elsewhere.links.length
    text = `${elsewhere.kind === 'patch' ? 'Overridden' : 'AP override'} on ${n} AP${n === 1 ? '' : 's'}`
  } else if (full) {
    text = 'Overridden by expert patch'
  } else {
    text = `Partly overridden: ${paths.join(', ')}`
  }
  return (
    <span className={cn('inline-flex max-w-full flex-wrap items-center gap-x-2 gap-y-1', className)}>
      <span className={cn(badgeBase, 'border-status-warn/50 bg-status-warn/10 whitespace-normal text-foreground')}>
        <Layers aria-hidden className="size-3.5 shrink-0 text-warning" />
        <span className="break-words">{text}</span>
      </span>
      {patch ? (
        <Link to={patch.to as never} className="text-xs text-brand underline-offset-4 hover:underline" data-inline-link="">
          {patch.label}
        </Link>
      ) : null}
      {elsewhere?.links.map((link) => (
        <Link key={link.to} to={link.to as never} className="text-xs text-brand underline-offset-4 hover:underline" data-inline-link="">
          {link.label}
        </Link>
      ))}
    </span>
  )
}

/** "Site default: 255" next to an AP field's Override switch (design §3.6). */
export function InheritedValue({ label = 'Site default', value, className }: { label?: string; value: ReactNode; className?: string }) {
  return (
    <span className={cn('text-sm text-muted-foreground', className)}>
      {label}: <span className="font-medium text-foreground tabular-nums">{value}</span>
    </span>
  )
}

/** Marks a field whose value differs from the saved one. */
export function DirtyMarker({ className }: { className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-1 text-xs text-brand', className)}>
      <span aria-hidden className="size-1.5 rounded-full bg-brand" />
      Changed
    </span>
  )
}
