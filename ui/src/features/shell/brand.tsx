import { cn } from '@/lib/utils'

// The mark: a signal radiating from one corner of a tile. The tile is ink,
// the arcs are orchid; both come from tokens so the mark follows the theme.
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden className={cn('size-8 shrink-0', className)}>
      <rect width="32" height="32" rx="8" className="fill-primary dark:fill-muted" />
      <g fill="none" strokeLinecap="round" strokeWidth="2.4" className="stroke-brand">
        <path d="M10 16.5a5.5 5.5 0 0 1 5.5 5.5" />
        <path d="M10 11.5a10.5 10.5 0 0 1 10.5 10.5" opacity="0.8" />
        <path d="M10 6.5A15.5 15.5 0 0 1 25.5 22" opacity="0.55" />
      </g>
      <circle cx="10" cy="22" r="2.4" className="fill-brand" />
    </svg>
  )
}

export function Wordmark({ className }: { className?: string }) {
  return <span className={cn('text-[1.0625rem] font-semibold tracking-[-0.03em]', className)}>dismist</span>
}
