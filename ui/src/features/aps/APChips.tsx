import type { APView, PushEvent } from '@/gen/api.gen'
import { Chip, StatusChip } from '@/components/app/StatusChip'
import { useNow } from '@/lib/time'
import { offlineFor, pushChip } from './model'

/** The headline status chip, with the offline duration ("Offline · 12 m"). */
export function APStatus({ ap, className }: { ap: APView; className?: string }) {
  const now = useNow()
  return <StatusChip status={ap.status} suffix={ap.status === 'offline' ? offlineFor(ap, now) : undefined} className={className} />
}

/** The per-AP config apply chip driven by `push` stream events (phase 2). */
export function PushChip({ ap, push }: { ap: APView; push?: PushEvent }) {
  const chip = pushChip(push, ap)
  if (!chip) return null
  return (
    <Chip tone={chip.tone} className="max-w-full">
      <span title={push?.error || undefined}>{chip.label}</span>
    </Chip>
  )
}
