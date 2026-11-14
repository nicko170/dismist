import { useEffect, useState, type ReactNode } from 'react'
import { Collapsible } from 'radix-ui'
import { ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'

/**
 * A form section: a card with a heading, an anchor (`#security`) and, when
 * collapsible, a summary line ("Security · WPA2/WPA3 Personal"). Advanced
 * sections start collapsed (§3.9); a hash link to the section opens it.
 */
export function Section({
  id,
  title,
  summary,
  description,
  badges,
  collapsible = false,
  defaultOpen = true,
  forceOpen = false,
  children,
  className,
  actions,
}: {
  id: string
  title: string
  summary?: ReactNode
  description?: ReactNode
  badges?: ReactNode
  collapsible?: boolean
  defaultOpen?: boolean
  /** Open while it holds an error or a change. */
  forceOpen?: boolean
  children: ReactNode
  className?: string
  actions?: ReactNode
}) {
  const [open, setOpen] = useState(() => defaultOpen || (typeof window !== 'undefined' && window.location.hash === `#${id}`))
  useEffect(() => {
    const openFromHash = () => {
      if (window.location.hash !== `#${id}`) return
      setOpen(true)
      requestAnimationFrame(() => document.getElementById(id)?.scrollIntoView({ block: 'start' }))
    }
    if (window.location.hash === `#${id}`) requestAnimationFrame(() => document.getElementById(id)?.scrollIntoView({ block: 'start' }))
    window.addEventListener('hashchange', openFromHash)
    return () => window.removeEventListener('hashchange', openFromHash)
  }, [id])
  const isOpen = open || forceOpen

  const headingId = `${id}-heading`
  if (!collapsible) {
    return (
      <section id={id} aria-labelledby={headingId} className={cn('scroll-mt-20 rounded-xl border bg-card', className)}>
        <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2 px-4 pt-4 md:px-5">
          <div className="grid min-w-0 gap-1">
            <h2 id={headingId} className="flex flex-wrap items-center gap-2 text-base font-semibold">
              {title}
              {badges}
            </h2>
            {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
          </div>
          {actions}
        </div>
        <div className="grid gap-5 px-4 pt-4 pb-5 md:px-5">{children}</div>
      </section>
    )
  }
  return (
    <Collapsible.Root asChild open={isOpen} onOpenChange={setOpen}>
      <section id={id} aria-labelledby={headingId} className={cn('scroll-mt-20 rounded-xl border bg-card', className)}>
        <h2 id={headingId} className="text-base font-semibold">
          <Collapsible.Trigger className="flex min-h-14 w-full cursor-pointer items-center gap-3 rounded-xl px-4 py-3 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/30 md:px-5">
            <span className="grid min-w-0 flex-1 gap-0.5">
              <span className="flex flex-wrap items-center gap-2">
                {title}
                {badges}
              </span>
              {summary ? <span className="truncate text-sm font-normal text-muted-foreground">{summary}</span> : null}
            </span>
            <ChevronDown aria-hidden className={cn('size-4 shrink-0 text-muted-foreground transition-transform duration-150', isOpen && 'rotate-180')} />
          </Collapsible.Trigger>
        </h2>
        <Collapsible.Content className="grid gap-5 border-t px-4 pt-4 pb-5 md:px-5">
          {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
          {children}
        </Collapsible.Content>
      </section>
    </Collapsible.Root>
  )
}

/** A titled group of related fields inside a section. */
export function FieldGroup({ title, children, className }: { title?: string; children: ReactNode; className?: string }) {
  return (
    <div className={cn('grid gap-4', className)}>
      {title ? <h3 className="text-sm font-semibold text-muted-foreground">{title}</h3> : null}
      {children}
    </div>
  )
}
