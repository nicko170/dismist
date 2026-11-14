import { createContext, lazy, Suspense, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import type { ConfirmOptions } from './ConfirmDialog'

// useConfirm(): a promise-returning confirmation. The provider is in the
// initial bundle, the dialog (Radix AlertDialog) loads on first use.

const ConfirmDialog = lazy(() => import('./ConfirmDialog').then((m) => ({ default: m.ConfirmDialog })))

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
        <Suspense fallback={null}>
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
        </Suspense>
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
