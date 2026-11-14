import { useState } from 'react'
import { FlaskConical } from 'lucide-react'
import { EXPERIMENTAL_HELP } from '@/components/app/badges'

// One-time acknowledgement per form (design P4): when a form uses an
// experimental value, Save stays disabled until the operator ticks "I
// understand". Remembered for the browser session, per form.

const PREFIX = 'dismist.ack:'

function stored(key: string): boolean {
  try {
    return sessionStorage.getItem(PREFIX + key) === '1'
  } catch {
    return false
  }
}

export function useExperimentalAck(formKey: string, inUse: boolean) {
  const [acknowledged, setAcknowledged] = useState(() => stored(formKey))
  const acknowledge = (value: boolean) => {
    setAcknowledged(value)
    try {
      if (value) sessionStorage.setItem(PREFIX + formKey, '1')
      else sessionStorage.removeItem(PREFIX + formKey)
    } catch {
      /* private mode: remembered for this page only */
    }
  }
  const required = inUse && !acknowledged
  const node = inUse ? (
    <div className="grid gap-2 rounded-lg border border-brand/35 bg-brand-soft px-4 py-3">
      <p className="flex items-center gap-2 text-sm font-medium">
        <FlaskConical aria-hidden className="size-4 text-brand" />
        This form uses experimental settings
      </p>
      <p className="text-sm text-muted-foreground">{EXPERIMENTAL_HELP} dismist checks that the AP applied them after saving and offers a one-click revert.</p>
      <label className="flex min-h-11 cursor-pointer items-center gap-3 text-sm fine:md:min-h-0">
        <input type="checkbox" checked={acknowledged} onChange={(event) => acknowledge(event.target.checked)} className="size-5 shrink-0 cursor-pointer accent-brand fine:md:size-4" />I understand
      </label>
    </div>
  ) : null
  return { required, acknowledged, acknowledge, node }
}
