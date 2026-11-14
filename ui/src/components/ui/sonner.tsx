import { Toaster as Sonner, type ToasterProps } from 'sonner'
import { useTheme } from '@/features/shell/theme'

// Toasts sit above the bottom tab bar on phones.
export function Toaster(props: ToasterProps) {
  const { resolved } = useTheme()
  return (
    <Sonner
      theme={resolved}
      position="bottom-center"
      offset={{ bottom: 'calc(var(--shell-tabs) + env(safe-area-inset-bottom) + 12px)' }}
      mobileOffset={{ bottom: 'calc(var(--shell-tabs) + env(safe-area-inset-bottom) + 12px)' }}
      toastOptions={{
        classNames: {
          toast: 'rounded-lg border bg-popover text-popover-foreground shadow-lg font-sans',
          description: 'text-muted-foreground',
        },
      }}
      style={
        {
          '--normal-bg': 'var(--popover)',
          '--normal-text': 'var(--popover-foreground)',
          '--normal-border': 'var(--border)',
        } as React.CSSProperties
      }
      {...props}
    />
  )
}
