import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { ArrowDown, ArrowUp, ArrowUpDown } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Skeleton } from '@/components/ui/skeleton'

// Lists (design §3.0, §7.3, §7.7 rule 4): a table from md, stacked cards
// below. Rows and cards are real links (stretched-link pattern), so the whole
// row is the target and keyboard users tab once per row. Every table column
// has a priority, decided by the list's own width (a container query), not
// the viewport, so the same list fits beside the icon rail at 768, beside the
// sidebar at 1024, and inside a drawer:
//   priority 1: always in the table;
//   priority 2: when the list is at least 44rem (704 px) wide;
//   priority 3: when the list is at least 60rem (960 px) wide.
// The page never scrolls sideways: the table is fixed-layout, cells wrap or
// truncate, identifiers break anywhere.

export type ListColumn<T> = {
  id: string
  /** Column header, and the label of the value on a phone card. */
  header: string
  cell: (row: T) => ReactNode
  priority?: 1 | 2 | 3
  align?: 'start' | 'end'
  /** Table width (any CSS length, e.g. '8rem' or '14%'); unset columns share the rest. */
  width?: string
  /**
   * Where the value goes on a phone card: the title (once per list; it holds
   * the row link), the top-right aside (a status chip), a line under the
   * title, a labelled value in the card's grid, or nowhere. Defaults: the
   * first column is the title, priority-1 columns are meta, others none.
   */
  card?: 'title' | 'aside' | 'subtitle' | 'meta' | 'none'
  /** Show the header as a sort button; the parent sorts the rows. */
  sortable?: boolean
  /** Truncate to one line (with the full value in title) instead of wrapping. */
  truncate?: boolean
  className?: string
}

export type ListSort = { id: string; desc: boolean }

export type RowLink = { to: string; params?: Record<string, string>; search?: Record<string, unknown>; hash?: string }

type Props<T> = {
  rows: readonly T[]
  columns: readonly ListColumn<T>[]
  rowKey: (row: T) => string
  /** Accessible name for the table and the card list, e.g. "Access points". */
  label: string
  /** The row's detail route; omit for rows that are not links. */
  rowLink?: (row: T) => RowLink
  /** Accessible name of the row link (defaults to the text of the title cell). */
  rowLabel?: (row: T) => string
  /** Trailing per-row actions (a ⋯ menu), kept above the stretched link. */
  renderActions?: (row: T) => ReactNode
  /** A coloured edge for rows that need attention; the row's text must still say why. */
  rowTone?: (row: T) => 'crit' | 'warn' | undefined
  sort?: ListSort
  onSortChange?: (sort: ListSort) => void
  /** Rendered instead of the list when there are no rows. */
  empty?: ReactNode
  className?: string
}

const priorityCell = {
  1: '',
  2: 'hidden @min-[44rem]:table-cell',
  3: 'hidden @min-[60rem]:table-cell',
} as const

function cardRole<T>(column: ListColumn<T>, index: number): NonNullable<ListColumn<T>['card']> {
  if (column.card) return column.card
  if (index === 0) return 'title'
  return (column.priority ?? 1) === 1 ? 'meta' : 'none'
}

const toneEdge = {
  crit: 'before:bg-status-crit',
  warn: 'before:bg-status-warn',
} as const

/** Interactive descendants (copy buttons, menus, inline links) sit above the stretched link. */
const raiseControls = '[&_a:not([data-row-link])]:relative [&_a:not([data-row-link])]:z-10 [&_button]:relative [&_button]:z-10'

/**
 * The stretched link: its ::after covers the row (the nearest positioned
 * ancestor), and the focus ring is drawn on that ::after so it outlines the
 * whole row or card.
 */
const stretched =
  'outline-none after:absolute after:inset-0 after:z-0 after:rounded-[inherit] focus-visible:after:outline-2 focus-visible:after:-outline-offset-2 focus-visible:after:outline-ring'

function RowAnchor({ link, label, children }: { link: RowLink; label?: string; children: ReactNode }) {
  return (
    <Link
      to={link.to as never}
      params={link.params as never}
      search={link.search as never}
      hash={link.hash}
      data-row-link=""
      aria-label={label}
      className={cn(stretched, 'font-medium text-foreground decoration-foreground/30 underline-offset-4 hover:underline')}
    >
      {children}
    </Link>
  )
}

function SortHeader({ column, sort, onSortChange }: { column: { id: string; header: string }; sort?: ListSort; onSortChange?: (s: ListSort) => void }) {
  const active = sort?.id === column.id
  const Icon = active ? (sort?.desc ? ArrowDown : ArrowUp) : ArrowUpDown
  return (
    <button
      type="button"
      className="-mx-1.5 inline-flex h-8 max-w-full cursor-pointer items-center gap-1 rounded px-1.5 hover:text-foreground"
      onClick={() => onSortChange?.({ id: column.id, desc: active ? !sort?.desc : false })}
    >
      <span className="truncate">{column.header}</span>
      <Icon aria-hidden className={cn('size-3.5 shrink-0', !active && 'opacity-40')} />
    </button>
  )
}

export function ResponsiveList<T>({
  rows,
  columns,
  rowKey,
  label,
  rowLink,
  rowLabel,
  renderActions,
  rowTone,
  sort,
  onSortChange,
  empty,
  className,
}: Props<T>) {
  if (!rows.length && empty) return empty
  const roles = columns.map((column, index) => cardRole(column, index))
  const titleIndex = Math.max(0, roles.indexOf('title'))
  const cell = (column: ListColumn<T>, row: T) => {
    const content = column.cell(row)
    return column.truncate ? (
      <span className="block truncate" title={typeof content === 'string' ? content : undefined}>
        {content}
      </span>
    ) : (
      content
    )
  }
  const titleContent = (row: T) => {
    const content = cell(columns[titleIndex], row)
    const link = rowLink?.(row)
    return link ? (
      <RowAnchor link={link} label={rowLabel?.(row)}>
        {content}
      </RowAnchor>
    ) : (
      content
    )
  }

  return (
    <div className={cn('@container min-w-0', className)} data-responsive-list="">
      {/* md and up: a fixed-layout table with column priorities. */}
      <div className="overflow-hidden rounded-lg border bg-card max-md:hidden">
        <table aria-label={label} className="w-full table-fixed border-collapse text-sm">
          <thead>
            <tr className="border-b bg-muted/50 text-left text-xs text-muted-foreground">
              {columns.map((column) => {
                const active = sort?.id === column.id
                return (
                  <th
                    key={column.id}
                    scope="col"
                    style={column.width ? { width: column.width } : undefined}
                    aria-sort={column.sortable && active ? (sort?.desc ? 'descending' : 'ascending') : undefined}
                    className={cn(
                      'h-10 px-3 font-medium first:pl-4 last:pr-4',
                      column.align === 'end' && 'text-right',
                      priorityCell[column.priority ?? 1],
                    )}
                  >
                    {column.sortable && onSortChange ? <SortHeader column={column} sort={sort} onSortChange={onSortChange} /> : column.header}
                  </th>
                )
              })}
              {renderActions ? (
                <th scope="col" className="w-14 pr-2">
                  <span className="sr-only">Actions</span>
                </th>
              ) : null}
            </tr>
          </thead>
          <tbody className="divide-y">
            {rows.map((row) => {
              const tone = rowTone?.(row)
              return (
                <tr
                  key={rowKey(row)}
                  data-row=""
                  className={cn(
                    'relative align-middle transition-colors duration-150',
                    rowLink && 'hover:bg-accent/45',
                    tone && ['before:absolute before:inset-y-0 before:left-0 before:w-[3px]', toneEdge[tone]],
                    raiseControls,
                  )}
                >
                  {columns.map((column, index) => (
                    <td
                      key={column.id}
                      className={cn(
                        'overflow-hidden px-3 py-2.5 break-words first:pl-4 last:pr-4',
                        column.align === 'end' && 'text-right tabular-nums',
                        priorityCell[column.priority ?? 1],
                        column.className,
                      )}
                    >
                      {index === titleIndex ? titleContent(row) : cell(column, row)}
                    </td>
                  ))}
                  {renderActions ? <td className="py-1 pr-2 text-right">{renderActions(row)}</td> : null}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {/* Below md: one bordered stack of cards. */}
      <ul aria-label={label} className="divide-y overflow-hidden rounded-lg border bg-card md:hidden">
        {rows.map((row) => {
          const tone = rowTone?.(row)
          const pick = (role: string) => columns.filter((_, index) => roles[index] === role)
          const meta = pick('meta')
          return (
            <li
              key={rowKey(row)}
              data-row=""
              className={cn(
                'relative grid min-h-14 gap-1.5 px-4 py-3 transition-colors duration-150',
                rowLink && 'active:bg-accent/60',
                tone && ['before:absolute before:inset-y-0 before:left-0 before:w-[3px]', toneEdge[tone]],
                raiseControls,
              )}
            >
              <div className="flex min-w-0 items-start gap-3">
                <div className="min-w-0 flex-1 text-base break-words">{titleContent(row)}</div>
                {pick('aside').map((column) => (
                  <div key={column.id} className="shrink-0">
                    {cell(column, row)}
                  </div>
                ))}
                {renderActions ? <div className="-my-2 -mr-2 shrink-0">{renderActions(row)}</div> : null}
              </div>
              {pick('subtitle').map((column) => (
                <div key={column.id} className="min-w-0 text-sm break-words text-muted-foreground">
                  {cell(column, row)}
                </div>
              ))}
              {meta.length ? (
                <dl className="grid grid-cols-2 gap-x-4 gap-y-1.5 pt-0.5 text-sm">
                  {meta.map((column) => (
                    <div key={column.id} className="min-w-0">
                      <dt className="text-xs text-muted-foreground">{column.header}</dt>
                      <dd className="min-w-0 break-words">{cell(column, row)}</dd>
                    </div>
                  ))}
                </dl>
              ) : null}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

/** The loading state of a ResponsiveList: same frame, placeholder rows. */
export function ResponsiveListSkeleton({ rows = 4, columns = 4, label = 'Loading' }: { rows?: number; columns?: number; label?: string }) {
  return (
    <div aria-busy="true" aria-label={label} role="status">
      <div className="overflow-hidden rounded-lg border bg-card max-md:hidden">
        <div className="h-10 border-b bg-muted/50" />
        {Array.from({ length: rows }, (_row, row) => (
          <div key={row} className="flex items-center gap-6 border-b px-4 py-3.5 last:border-0">
            {Array.from({ length: columns }, (_col, col) => (
              <Skeleton key={col} className={cn('h-4', col === 0 ? 'w-1/4' : 'flex-1')} />
            ))}
          </div>
        ))}
      </div>
      <div className="divide-y overflow-hidden rounded-lg border bg-card md:hidden">
        {Array.from({ length: rows }, (_, row) => (
          <div key={row} className="grid gap-2 px-4 py-3.5">
            <Skeleton className="h-5 w-2/5" />
            <div className="grid grid-cols-2 gap-4">
              <Skeleton className="h-4" />
              <Skeleton className="h-4" />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
