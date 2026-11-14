import { Link } from '@tanstack/react-router'
import { toast } from 'sonner'
import type { PendingAP } from '@/gen/api.gen'
import { useDismissPending } from '@/api/queries/aps'
import { describeError } from '@/api/errors'
import { Button, buttonVariants } from '@/components/ui/button'
import { Chip } from '@/components/app/StatusChip'
import { Identifier, Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { Card } from '@/features/overview/kit/Card'

// APs that said hello but are not served (design §3.4 banner, §3.15 card 1):
// Register opens the onboarding form pre-filled; Dismiss hides the row for
// 30 days (its attempts keep counting). Two APs behind one NAT share the
// public address but differ in LAN IP and serial.

const reasonText: Record<PendingAP['reason'], { label: string; tone: 'warn' | 'crit' }> = {
  unregistered: { label: 'Not registered', tone: 'warn' },
  source_mismatch: { label: 'Source mismatch', tone: 'crit' },
  auth_failed: { label: 'Auth failed', tone: 'crit' },
}

export function PendingAPs({ pending, siteId }: { pending: PendingAP[]; siteId?: string }) {
  const dismiss = useDismissPending()
  if (!pending.length) return null
  return (
    <Card
      id="pending"
      title={pending.length === 1 ? '1 AP is trying to connect' : `${pending.length} APs are trying to connect`}
      description="Register an AP to serve it, or dismiss it for 30 days."
      bodyClassName="p-0"
    >
      <ul className="divide-y">
        {pending.map((ap) => {
          const reason = reasonText[ap.reason] ?? reasonText.unregistered
          return (
            <li key={ap.id} className="grid min-w-0 gap-3 px-4 py-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-center">
              <div className="grid min-w-0 gap-1">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <Identifier value={ap.id} className="font-medium" />
                  <Chip tone={reason.tone}>{reason.label}</Chip>
                </div>
                <p className="text-xs break-words text-muted-foreground">
                  {[ap.model, ap.sku && ap.sku !== ap.model ? ap.sku : '', ap.serial ? `serial ${ap.serial}` : '', ap.firmware].filter(Boolean).join(' · ')}
                </p>
                <p className="text-xs text-muted-foreground">
                  LAN <Mono>{ap.local_ip || '–'}</Mono> · via <Mono>{ap.remote_address || '–'}</Mono> · {ap.attempts}{' '}
                  {ap.attempts === 1 ? 'attempt' : 'attempts'} · first seen <RelativeTime value={ap.first_seen} /> · last <RelativeTime value={ap.last_seen} />
                </p>
              </div>
              <div className="flex gap-2">
                <Link
                  to={'/onboard/register' as never}
                  search={{ id: ap.id, site: siteId } as never}
                  className={buttonVariants({ variant: 'brand', size: 'sm' })}
                  aria-label={`Register ${ap.id}`}
                >
                  Register
                </Link>
                <Button
                  variant="outline"
                  size="sm"
                  aria-label={`Dismiss ${ap.id} for 30 days`}
                  disabled={dismiss.isPending && dismiss.variables?.id === ap.id}
                  onClick={() =>
                    dismiss.mutate(
                      { id: ap.id },
                      {
                        onSuccess: () => toast.success(`${ap.id} dismissed for 30 days`),
                        onError: (error) => toast.error(`Could not dismiss ${ap.id}`, { description: describeError(error).message }),
                      },
                    )
                  }
                >
                  Dismiss
                </Button>
              </div>
            </li>
          )
        })}
      </ul>
    </Card>
  )
}
