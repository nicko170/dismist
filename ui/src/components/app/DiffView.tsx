import { lazy, Suspense } from 'react'
import type { DiffEntry } from '@/api/types'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

// What a change does to the compiled config (design §4.8 dry run `diff`,
// §3.13 change diffs). Each entry is a firmware path with its old and new
// value; secrets arrive as "[changed]" and are never shown. Before/after
// carry text markers and labels, so colour is never the only signal.

const SECRET = '[changed]'

/** One value as compact text: JSON for objects, "absent" for a missing key. */
export function formatDiffValue(value: unknown): string {
  if (value === undefined) return 'absent'
  if (value === SECRET || value === '[redacted]') return 'changed (secret, not shown)'
  if (typeof value === 'string') return JSON.stringify(value)
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

/** "Changed", "Added" or "Removed", from which side is absent. */
export function diffKind(entry: DiffEntry): 'added' | 'removed' | 'changed' {
  if (entry.from === undefined || entry.from === null) return entry.to === undefined || entry.to === null ? 'changed' : 'added'
  if (entry.to === undefined || entry.to === null) return 'removed'
  return 'changed'
}

export function DiffView({
  entries,
  label = 'Changes',
  empty = 'No changes.',
  className,
}: {
  entries: readonly DiffEntry[]
  label?: string
  empty?: string
  className?: string
}) {
  if (!entries.length) return <p className={cn('text-sm text-muted-foreground', className)}>{empty}</p>
  return (
    <ul aria-label={label} className={cn('divide-y overflow-hidden rounded-md border bg-card text-sm', className)}>
      {entries.map((entry) => {
        const kind = diffKind(entry)
        return (
          <li key={entry.path} className="grid gap-1.5 px-3 py-2.5">
            <div className="flex min-w-0 items-baseline justify-between gap-3">
              <code className="min-w-0 font-mono text-[0.8125rem] font-medium break-all">{entry.path}</code>
              <span className="shrink-0 text-xs text-muted-foreground">{kind === 'added' ? 'Added' : kind === 'removed' ? 'Removed' : 'Changed'}</span>
            </div>
            <div className="grid gap-1 font-mono text-[0.8125rem] md:grid-cols-2 md:gap-2">
              {kind !== 'added' ? (
                <p className="flex min-w-0 gap-2 rounded-sm bg-status-crit/8 px-2 py-1">
                  <span aria-hidden className="text-destructive select-none">−</span>
                  <span className="sr-only">Before: </span>
                  <span className="min-w-0 break-all">{formatDiffValue(entry.from)}</span>
                </p>
              ) : null}
              {kind !== 'removed' ? (
                <p className="flex min-w-0 gap-2 rounded-sm bg-status-good/10 px-2 py-1 md:col-start-2">
                  <span aria-hidden className="text-success select-none">+</span>
                  <span className="sr-only">After: </span>
                  <span className="min-w-0 break-all">{formatDiffValue(entry.to)}</span>
                </p>
              ) : null}
            </div>
          </li>
        )
      })}
    </ul>
  )
}

const JsonDiffEditor = lazy(() => import('./code/JsonDiffEditor'))

/**
 * A line diff of two JSON documents (Compare with applied, expert patch
 * against the saved one). CodeMirror's merge view loads on first use.
 */
export function JsonDiff({ before, after, label, className }: { before: string; after: string; label: string; className?: string }) {
  return (
    <Suspense fallback={<Skeleton className={cn('h-64 w-full', className)} />}>
      <JsonDiffEditor before={before} after={after} label={label} className={className} />
    </Suspense>
  )
}
