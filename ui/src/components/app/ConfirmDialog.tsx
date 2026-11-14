import { createContext, useCallback, useContext, useId, useRef, useState, type ReactNode } from 'react'
import { Loader2 } from 'lucide-react'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { describeError } from '@/api/errors'

// Confirmations for disruptive actions (design P6, §3.0): the title names
// the object, the description names the impact ("19 clients will be
// disconnected from 1 AP"). `confirmText` asks the operator to type a name
// first (Delete AP). The dialog stays open while `onConfirm` runs and shows
// its error in place; it closes when the promise resolves.

export type ConfirmOptions = {
  title: ReactNode
  description?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  tone?: 'default' | 'destructive'
  /** The operator must type this exactly before confirming. */
  confirmText?: string
  /** Extra controls (for example "Also delete history"). */
  children?: ReactNode
}

export function ConfirmDialog({
  open,
  onOpenChange,
  onConfirm,
  title,
  description,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  tone = 'default',
  confirmText,
  children,
}: ConfirmOptions & {
  open: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => unknown | Promise<unknown>
}) {
  const [typed, setTyped] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const inputId = useId()
  const matches = !confirmText || typed.trim() === confirmText

  const close = (next: boolean) => {
    if (pending) return
    if (!next) {
      setTyped('')
      setError(null)
    }
    onOpenChange(next)
  }

  const run = async () => {
    if (!matches) return
    setPending(true)
    setError(null)
    try {
      await onConfirm()
      setPending(false)
      setTyped('')
      onOpenChange(false)
    } catch (cause) {
      setPending(false)
      setError(describeError(cause).message)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={close}>
      <AlertDialogContent>
        <div className="grid gap-2">
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description ? <AlertDialogDescription>{description}</AlertDialogDescription> : null}
        </div>
        {children}
        {confirmText ? (
          <div className="grid gap-1.5">
            <label htmlFor={inputId} className="text-sm">
              Type <span className="font-mono font-medium break-all">{confirmText}</span> to confirm
            </label>
            <Input
              id={inputId}
              value={typed}
              onChange={(event) => setTyped(event.target.value)}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              autoComplete="off"
            />
          </div>
        ) : null}
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <AlertDialogCancel asChild>
            <Button variant="outline" disabled={pending}>
              {cancelLabel}
            </Button>
          </AlertDialogCancel>
          <Button variant={tone === 'destructive' ? 'destructive' : 'brand'} onClick={run} disabled={!matches || pending}>
            {pending ? <Loader2 aria-hidden className="animate-spin" /> : null}
            {confirmLabel}
          </Button>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  )
}

type Pending = ConfirmOptions & { resolve: (ok: boolean) => void }
const ConfirmContext = createContext<((options: ConfirmOptions) => Promise<boolean>) | null>(null)

/** Mounted once in main.tsx; `useConfirm()` opens its dialog. */
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [current, setCurrent] = useState<Pending | null>(null)
  const [open, setOpen] = useState(false)
  const resolver = useRef<((ok: boolean) => void) | null>(null)
  const confirm = useCallback(
    (options: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        resolver.current?.(false)
        resolver.current = resolve
        setCurrent({ ...options, resolve })
        setOpen(true)
      }),
    [],
  )
  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      {current ? (
        <ConfirmDialog
          {...current}
          open={open}
          onOpenChange={(next) => {
            setOpen(next)
            if (!next && resolver.current) {
              resolver.current(false)
              resolver.current = null
            }
          }}
          onConfirm={() => {
            resolver.current?.(true)
            resolver.current = null
          }}
        />
      ) : null}
    </ConfirmContext.Provider>
  )
}

/**
 * `if (await confirm({ title: 'Disable INTERNET?', tone: 'destructive' })) …`
 * For actions whose errors should show inside the dialog, render
 * ConfirmDialog with an async onConfirm instead.
 */
export function useConfirm() {
  const confirm = useContext(ConfirmContext)
  if (!confirm) throw new Error('useConfirm needs <ConfirmProvider>')
  return confirm
}
