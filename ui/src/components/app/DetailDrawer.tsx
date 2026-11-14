import type { ReactNode } from 'react'
import { Dialog, DialogContent } from '@/components/ui/dialog'

// A side panel from md and a full-height sheet on phones (design §7.3), for
// details that do not deserve their own route: one event's data, a
// neighbour, a change entry. Focus is trapped and returned on close.

export function DetailDrawer({
  open,
  onOpenChange,
  title,
  description,
  footer,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  footer?: ReactNode
  children: ReactNode
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent side="right" title={title} description={description} footer={footer}>
        {children}
      </DialogContent>
    </Dialog>
  )
}
