import { Loader2 } from 'lucide-react'
import { AlertDialog, AlertDialogContent, AlertDialogDescription, AlertDialogTitle } from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'

// 409 `stale` (design §4.8 "Concurrency", §7.4): someone saved this object
// after the form loaded. Reload discards the edits and shows the saved
// version; Overwrite saves the edits over it.

export function StaleDialog({
  open,
  objectName = 'this',
  onReload,
  onOverwrite,
  onCancel,
  pending = false,
}: {
  open: boolean
  objectName?: string
  onReload: () => void
  onOverwrite: () => void
  onCancel: () => void
  pending?: boolean
}) {
  return (
    <AlertDialog open={open} onOpenChange={(next) => !next && !pending && onCancel()}>
      <AlertDialogContent>
        <div className="grid gap-2">
          <AlertDialogTitle>This changed since you opened it</AlertDialogTitle>
          <AlertDialogDescription>
            Someone saved {objectName} after you started editing. Reload to see their version (your edits here are discarded), or overwrite it with yours.
          </AlertDialogDescription>
        </div>
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button variant="outline" onClick={onReload} disabled={pending}>
            Reload
          </Button>
          <Button variant="destructive" onClick={onOverwrite} disabled={pending}>
            {pending ? <Loader2 aria-hidden className="animate-spin" /> : null}
            Overwrite
          </Button>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  )
}
