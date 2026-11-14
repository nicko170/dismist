import { toast } from 'sonner'
import { describeError } from '@/api/errors'

// Toasts (design §3.0 "Saving"): success and failure of a save, and Undo for
// reversible one-click actions (WLAN enable/disable, 10 s). The Toaster is
// polite (aria-live) and sits above the phone tab bar.

export const notify = {
  success(message: string, description?: string) {
    toast.success(message, { description })
  },
  error(error: unknown, title = 'That did not work') {
    const { message, code } = describeError(error)
    toast.error(title, { description: code ? `${message} (${code})` : message })
  },
  info(message: string, description?: string) {
    toast(message, { description })
  },
  /** A toast with an Undo button that stays for `ms` (default 10 s). */
  undo(message: string, onUndo: () => void, ms = 10_000) {
    toast(message, { duration: ms, action: { label: 'Undo', onClick: onUndo } })
  },
}
