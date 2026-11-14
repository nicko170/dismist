import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { cn } from '@/lib/utils'

// Tables at md and up, stacked cards below (design §3.0 "Lists", §7.7 rule
// 4). Every column has a priority: 1 shows from md, 2 from lg, 3 from xl,
// so nothing scrolls sideways. Rows and cards are real links (the
// stretched-link pattern, §7.8); controls inside a row sit above the link.

export type RowLink = { to: string; params?: Record<string, string>; search?: Record<string, unknown>; label: ReactNode }

export type Column<T> = {
  id: string
  header: ReactNode
  render: (row: T) => ReactNode
  priority?: 1 | 2 | 3
  /** The column that carries the row link (default: the first). */
  primary?: boolean
  align?: 'start' | 'end'
  className?: string
}

const priorityClass = { 1: '', 2: 'hidden lg:table-cell', 3: 'hidden xl:table-cell' } as const

export function RowLinkEl({ link, className }: { link: RowLink; className?: string }) {
  return (
    <Link
      to={link.to as never}
      params={link.params as never}
      search={link.search as never}
      className={cn(
        'font-medium break-words outline-none after:absolute after:inset-0 after:content-[""] hover:underline focus-visible:after:rounded-lg focus-visible:after:ring-3 focus-visible:after:ring-ring/40',
        className,
      )}
    >
      {link.label}
    </Link>
  )
}

export function ResponsiveList<T>({
  rows,
  rowKey,
  columns,
  renderCard,
  rowLink,
  caption,
  rowClassName,
  className,
}: {
  rows: readonly T[]
  rowKey: (row: T) => string
  columns: Column<T>[]
  /** Phone card body; `title` is the stretched link to place on line 1. */
  renderCard: (row: T, title: ReactNode) => ReactNode
  rowLink?: (row: T) => RowLink
  caption: string
  rowClassName?: (row: T) => string | undefined
  className?: string
}) {
  const primary = columns.find((column) => column.primary) ?? columns[0]
  return (
    <div className={cn('min-w-0', className)}>
      <ul aria-label={caption} className="grid gap-2 md:hidden">
        {rows.map((row) => {
          const link = rowLink?.(row)
          const title = link ? <RowLinkEl link={link} /> : null
          return (
            <li
              key={rowKey(row)}
              className={cn(
                'relative min-w-0 rounded-lg border bg-card px-4 py-3 transition-colors has-[a:hover]:bg-accent/50',
                rowClassName?.(row),
              )}
            >
              {renderCard(row, title)}
            </li>
          )
        })}
      </ul>
      <div className="max-md:hidden overflow-hidden rounded-lg border bg-card">
        <table className="w-full table-fixed border-collapse text-sm">
          <caption className="sr-only">{caption}</caption>
          <thead>
            <tr className="border-b bg-muted/50 text-left text-xs text-muted-foreground">
              {columns.map((column) => (
                <th
                  key={column.id}
                  scope="col"
                  className={cn(
                    'h-10 px-3 font-medium first:pl-4 last:pr-4',
                    priorityClass[column.priority ?? 1],
                    column.align === 'end' && 'text-right',
                    column.className,
                  )}
                >
                  {column.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const link = rowLink?.(row)
              return (
                <tr
                  key={rowKey(row)}
                  className={cn('relative border-b last:border-b-0 transition-colors has-[a:hover]:bg-accent/50', rowClassName?.(row))}
                >
                  {columns.map((column) => (
                    <td
                      key={column.id}
                      className={cn(
                        'min-w-0 px-3 py-2.5 align-middle break-words first:pl-4 last:pr-4',
                        priorityClass[column.priority ?? 1],
                        column.align === 'end' && 'text-right tabular-nums',
                        column.className,
                      )}
                    >
                      {column === primary && link ? (
                        <div className="grid min-w-0 gap-0.5">
                          <RowLinkEl link={link} />
                          {column.render(row)}
                        </div>
                      ) : (
                        column.render(row)
                      )}
                    </td>
                  ))}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
