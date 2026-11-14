import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import type { NavTarget } from './nav'

// Feature routes land in later phases, so nav targets are plain strings
// rather than the router's typed paths; TanStack still marks the active link
// with aria-current="page" and data-status="active".
export function NavLink({
  target,
  className,
  children,
  onClick,
}: {
  target: NavTarget
  className?: string
  children: ReactNode
  onClick?: () => void
}) {
  return (
    <Link
      to={target.to as never}
      search={target.search as never}
      activeOptions={{ includeSearch: false }}
      className={className}
      onClick={onClick}
    >
      {children}
    </Link>
  )
}
