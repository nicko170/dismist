import { Dialog, DialogContent } from '@/components/ui/dialog'
import { shortcutRows } from './shortcuts'

/** The "?" dialog (lazy-loaded). */
export default function ShortcutHelp({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent title="Keyboard shortcuts" description="Available when no text field has focus.">
        <dl className="grid gap-2">
          {shortcutRows.map((row) => (
            <div key={row.label} className="flex items-center justify-between gap-4 text-sm">
              <dt>{row.label}</dt>
              <dd className="flex shrink-0 items-center gap-1">
                {row.keys.map((key, index) => (
                  <span key={key} className="flex items-center gap-1">
                    {index ? <span className="text-xs text-muted-foreground">then</span> : null}
                    <kbd className="min-w-6 rounded border bg-muted px-1.5 py-0.5 text-center font-mono text-xs">{key}</kbd>
                  </span>
                ))}
              </dd>
            </div>
          ))}
        </dl>
      </DialogContent>
    </Dialog>
  )
}
