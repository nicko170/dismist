import { useEffect, useId, useState, type ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { cn } from '@/lib/utils'

// A card that groups one topic of a page or form (design §7.3 `Section`).
// Collapsible sections show a one-line summary while closed ("WPA2/WPA3
// Personal · PMF optional"), so a long form reads as a table of contents on a
// phone. `id` makes the section a hash anchor (#security, design §2.5): a
// matching hash opens it and scrolls it into view.

export function Section({
  id,
  title,
  summary,
  description,
  actions,
  badges,
  collapsible = false,
  defaultOpen = true,
  open: openProp,
  onOpenChange,
  className,
  children,
}: {
  id?: string
  title: ReactNode
  /** Shown beside the title while collapsed, and under it on phones. */
  summary?: ReactNode
  description?: ReactNode
  /** Buttons at the right of the header (kept outside the collapse trigger). */
  actions?: ReactNode
  /** Experimental, shadowed or dirty markers next to the title. */
  badges?: ReactNode
  collapsible?: boolean
  defaultOpen?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
  className?: string
  children?: ReactNode
}) {
  const headingId = useId()
  const [openState, setOpenState] = useState(defaultOpen)
  const open = openProp ?? openState
  const setOpen = (next: boolean) => {
    setOpenState(next)
    onOpenChange?.(next)
  }

  // Open and reveal the section its hash names.
  useEffect(() => {
    if (!id) return
    const reveal = () => {
      if (window.location.hash !== `#${id}`) return
      setOpenState(true)
      onOpenChange?.(true)
      requestAnimationFrame(() => document.getElementById(id)?.scrollIntoView({ block: 'start' }))
    }
    reveal()
    window.addEventListener('hashchange', reveal)
    return () => window.removeEventListener('hashchange', reveal)
    // onOpenChange is a callback prop; re-running on its identity would re-open on every render.
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  const heading = (
    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
      <span id={headingId} className="text-base font-semibold">
        {title}
      </span>
      {badges}
    </span>
  )

  const body = (
    <div className="grid gap-4 px-4 pt-1 pb-4 md:px-5 md:pb-5">
      {description ? <p className="max-w-prose text-sm text-muted-foreground">{description}</p> : null}
      {children}
    </div>
  )

  if (!collapsible) {
    return (
      <section id={id} aria-labelledby={headingId} className={cn('scroll-mt-20 rounded-lg border bg-card', className)}>
        <header className="flex min-h-14 flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 pt-3 pb-2 md:px-5">
          <h2 className="min-w-0">{heading}</h2>
          {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
        </header>
        {body}
      </section>
    )
  }

  return (
    <Collapsible open={open} onOpenChange={setOpen} asChild>
      <section id={id} aria-labelledby={headingId} className={cn('scroll-mt-20 rounded-lg border bg-card', className)}>
        <header className="flex items-stretch gap-2 pr-2">
          <h2 className="min-w-0 flex-1">
            <CollapsibleTrigger className="group flex min-h-14 w-full cursor-pointer items-center gap-3 rounded-lg px-4 py-3 text-left outline-none focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring md:px-5">
              <span className="grid min-w-0 flex-1 gap-0.5">
                {heading}
                {summary && !open ? <span className="text-sm font-normal break-words text-muted-foreground">{summary}</span> : null}
              </span>
              <ChevronDown aria-hidden className="size-4 shrink-0 text-muted-foreground transition-transform duration-150 group-data-[state=open]:rotate-180" />
            </CollapsibleTrigger>
          </h2>
          {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
        </header>
        <CollapsibleContent>{body}</CollapsibleContent>
      </section>
    </Collapsible>
  )
}
