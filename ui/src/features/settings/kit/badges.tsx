import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { FlaskConical, Layers, PencilRuler } from 'lucide-react'
import type { CrossScopeShadow } from '@/api/queries/sites'
import { cn } from '@/lib/utils'

// Badges for experimental, shadowed and inherited values (design §3.0, §7.3).
// Text always accompanies the icon.

export const EXPERIMENTAL_HELP = 'Not yet verified on AP33 0.15. Try it on one AP in a quiet window first.'

const badgeClass =
  'inline-flex h-6 max-w-full shrink-0 items-center gap-1 rounded-full border px-2 text-xs font-medium whitespace-nowrap'

export function ExperimentalBadge({ className, label = 'Experimental' }: { className?: string; label?: string }) {
  return (
    <span className={cn(badgeClass, 'border-status-info/40 bg-status-info/10 text-foreground', className)} title={EXPERIMENTAL_HELP}>
      <FlaskConical aria-hidden className="size-3.5 text-info" />
      {label}
    </span>
  )
}

export function ShadowedBadge({
  paths,
  to,
  search,
  partial,
  className,
}: {
  paths?: string[]
  to?: string
  search?: Record<string, string>
  partial?: boolean
  className?: string
}) {
  const text = partial ? `Partly overridden by expert patch${paths?.length ? `: ${paths.join(', ')}` : ''}` : 'Overridden by expert patch'
  const content = (
    <>
      <PencilRuler aria-hidden className="size-3.5 shrink-0 text-warning" />
      <span className="truncate">{text}</span>
    </>
  )
  return to ? (
    <Link to={to as never} search={search as never} className={cn(badgeClass, 'border-status-warn/45 bg-status-warn/10 text-foreground underline-offset-2 hover:underline', className)}>
      {content}
    </Link>
  ) : (
    <span className={cn(badgeClass, 'border-status-warn/45 bg-status-warn/10 text-foreground', className)}>{content}</span>
  )
}

/** "Overridden on 1 AP" / "AP override on 2 APs" with links to those APs (§3.0 cross scope). */
export function CrossScopeBadge({ shadow, siteId }: { shadow: CrossScopeShadow; siteId: string }) {
  const n = shadow.aps.length
  const label = shadow.kind === 'patch' ? `Overridden on ${n} AP${n === 1 ? '' : 's'}` : `AP override on ${n} AP${n === 1 ? '' : 's'}`
  return (
    <span className={cn(badgeClass, 'h-auto min-h-6 flex-wrap border-border bg-muted py-0.5 whitespace-normal text-foreground')}>
      <Layers aria-hidden className="size-3.5 text-muted-foreground" />
      {label}:
      {shadow.aps.map((ap, index) => (
        <span key={ap.id}>
          <Link
            to={`/sites/${encodeURIComponent(siteId)}/aps/${encodeURIComponent(ap.id)}/${shadow.kind === 'patch' ? 'config' : 'settings'}` as never}
            search={(shadow.kind === 'patch' ? { view: 'expert' } : undefined) as never}
            className="text-brand underline-offset-2 hover:underline"
          >
            {ap.name}
          </Link>
          {index < n - 1 ? ',' : ''}
        </span>
      ))}
    </span>
  )
}

/** "Site default: 255" beside an AP-level Override switch (§3.6). */
export function InheritedValue({ children }: { children: ReactNode }) {
  return <span className="text-xs text-muted-foreground">Site default: {children}</span>
}
