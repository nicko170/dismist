import { useState, type ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/** One card on the onboarding hub: an H2, an optional badge and a body. */
export function Panel({
  id,
  title,
  badge,
  description,
  children,
  className,
}: {
  id: string
  title: string
  badge?: ReactNode
  description?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section aria-labelledby={`${id}-title`} className={cn('grid min-w-0 gap-4 rounded-xl border bg-card p-4 sm:p-5', className)}>
      <header className="grid gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <h2 id={`${id}-title`} className="text-base font-semibold">
            {title}
          </h2>
          {badge}
        </div>
        {description ? <div className="text-sm text-muted-foreground">{description}</div> : null}
      </header>
      {children}
    </section>
  )
}

/** A label, control, and help or error line wired with aria-describedby. */
export function FieldRow({
  id,
  label,
  optional,
  help,
  error,
  children,
}: {
  id: string
  label: string
  optional?: boolean
  help?: ReactNode
  error?: string
  children: ReactNode
}) {
  return (
    <div className="grid min-w-0 content-start gap-2">
      <label htmlFor={id} className="text-sm leading-none font-medium">
        {label}
        {optional ? <span className="ml-1.5 font-normal text-muted-foreground">(optional)</span> : null}
      </label>
      {children}
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : help ? (
        <p id={`${id}-help`} className="text-sm text-muted-foreground">
          {help}
        </p>
      ) : null}
    </div>
  )
}

/** aria-describedby for a FieldRow's control. */
export function describedBy(id: string, error: string | undefined, help: unknown): string | undefined {
  if (error) return `${id}-error`
  return help ? `${id}-help` : undefined
}

/** A block of copyable text (commands, cloud.ini): wraps, never scrolls the page sideways. */
export function CodeBlock({ children, label }: { children: string; label: string }) {
  return (
    <figure aria-label={label} className="m-0 min-w-0">
      <pre className="min-w-0 overflow-x-auto rounded-md bg-muted px-3 py-2.5 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap text-foreground">
        {children}
      </pre>
    </figure>
  )
}

/** Copies a long text (a command, a file); the accessible name stays short, unlike CopyButton's. */
export function CopyTextButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value)
          setCopied(true)
          window.setTimeout(() => setCopied(false), 1500)
        } catch {
          setCopied(false)
        }
      }}
    >
      {copied ? <Check className="text-success" /> : <Copy />}
      {copied ? 'Copied' : label}
    </Button>
  )
}
