import { useState, type ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/** MACs, IPs, BSSIDs and IDs: monospaced and allowed to wrap anywhere (design §3.0). */
export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn('font-mono text-[0.92em] break-all tabular-nums', className)}>{children}</span>
}

export function CopyButton({ value, label = 'Copy', className }: { value: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      className={cn('text-muted-foreground hover:text-foreground', className)}
      aria-label={copied ? 'Copied' : `${label} ${value}`}
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
    </Button>
  )
}

/** An identifier with its copy button (44 px hit area on touch). */
export function Identifier({ value, className }: { value: string; className?: string }) {
  return (
    <span className={cn('inline-flex min-w-0 items-center gap-0.5', className)}>
      <Mono>{value}</Mono>
      <CopyButton value={value} className="-my-2" />
    </span>
  )
}
