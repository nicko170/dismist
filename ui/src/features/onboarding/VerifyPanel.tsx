import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { CircleCheck, CircleDashed, CircleX, LoaderCircle, Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Identifier } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { isApiError } from '@/api/errors'
import { apDetailQuery } from '@/api/queries/onboarding'
import { useSiteStream } from '@/api/sse'
import { useNow } from '@/lib/time'
import { cn } from '@/lib/utils'
import { verifyFinished, verifySteps, type StepState, type VerifyStep } from './verify'

// The live Verify panel (design §3.15). It opens the AP's site stream, so
// `ap` events patch the AP query as they happen, and polls every 5 s until
// the AP is in sync or stopped by an error (the AP may not be in the
// stream's snapshot until it first connects).

const POLL_MS = 5_000

export function VerifyPanel({ apId, siteId }: { apId: string; siteId?: string }) {
  const [startedAt] = useState(() => Date.now())
  const ap = useQuery({
    ...apDetailQuery(apId),
    refetchInterval: (query) => (query.state.data && verifyFinished(verifySteps(query.state.data)) ? false : POLL_MS),
  })
  useSiteStream(ap.data?.site_id || siteId)
  const now = useNow()

  if (ap.isPending) return <SkeletonRows rows={4} />
  if (ap.isError) {
    if (isApiError(ap.error) && ap.error.status === 404) {
      return (
        <EmptyState title={`No AP with ID ${apId} is registered.`} action={<RegisterAgain siteId={siteId} />}>
          It may have been deleted, or the registration did not finish.
        </EmptyState>
      )
    }
    return <ErrorState error={ap.error} title="Could not load the AP" onRetry={() => void ap.refetch()} />
  }

  const steps = verifySteps(ap.data, Math.max(0, now - startedAt))
  const result = verifyFinished(steps)
  const current = steps.find((step) => step.state === 'error') ?? steps.find((step) => step.state === 'active') ?? steps.at(-1)!
  const site = ap.data.site_id || siteId

  return (
    <div className="grid gap-5">
      <div className="grid gap-1 text-sm">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="font-medium">{ap.data.name || ap.data.id}</span>
          <Identifier value={ap.data.id} />
        </div>
        <p className="text-muted-foreground">
          Last seen <RelativeTime value={ap.data.last_seen} fallback="never" />
          {ap.data.lan_ip ? (
            <>
              {' '}
              · LAN <span className="font-mono">{ap.data.lan_ip}</span>
            </>
          ) : null}
          {ap.data.remote_address ? (
            <>
              {' '}
              · from <span className="font-mono">{ap.data.remote_address}</span>
            </>
          ) : null}
        </p>
      </div>

      <p className="sr-only" aria-live="polite">
        {current.label}
      </p>
      <ol className="grid gap-0" aria-label="Verification steps">
        {steps.map((step, index) => (
          <StepItem key={step.id} step={step} last={index === steps.length - 1} />
        ))}
      </ol>

      <div className="flex flex-wrap gap-2">
        {result === 'ok' && site ? (
          <Button asChild>
            <Link to={`/sites/${encodeURIComponent(site)}/aps/${encodeURIComponent(ap.data.id)}` as never}>Open {ap.data.name || 'AP'}</Link>
          </Button>
        ) : null}
        <RegisterAgain siteId={site} />
        <Button asChild variant="ghost">
          <Link to="/onboard" search={{ site }}>
            Back to Add an AP
          </Link>
        </Button>
      </div>
    </div>
  )
}

function RegisterAgain({ siteId }: { siteId?: string }) {
  return (
    <Button asChild variant="outline">
      <Link to="/onboard/register" search={{ site: siteId }}>
        <Plus /> Register another AP
      </Link>
    </Button>
  )
}

const icon: Record<StepState, React.ReactNode> = {
  done: <CircleCheck aria-hidden className="size-5 text-success" />,
  active: <LoaderCircle aria-hidden className="size-5 animate-spin text-info motion-reduce:animate-none" />,
  waiting: <CircleDashed aria-hidden className="size-5 text-muted-foreground" />,
  error: <CircleX aria-hidden className="size-5 text-destructive" />,
}

const stateText: Record<StepState, string> = { done: 'Done', active: 'In progress', waiting: 'Not started', error: 'Failed' }

function StepItem({ step, last }: { step: VerifyStep; last: boolean }) {
  return (
    <li className="grid grid-cols-[1.25rem_1fr] gap-x-3" aria-current={step.state === 'active' ? 'step' : undefined} data-state={step.state}>
      <div className="flex flex-col items-center">
        {icon[step.state]}
        {last ? null : <span aria-hidden className={cn('my-1 w-px flex-1', step.state === 'done' ? 'bg-success/50' : 'bg-border')} />}
      </div>
      <div className={cn('grid min-w-0 gap-1', last ? 'pb-0' : 'pb-5')}>
        <p className={cn('text-sm font-medium break-words', step.state === 'waiting' && 'text-muted-foreground', step.state === 'error' && 'text-destructive')}>
          <span className="sr-only">{stateText[step.state]}: </span>
          {step.label}
        </p>
        {step.detail ? <p className="text-sm break-words text-muted-foreground">{step.detail}</p> : null}
      </div>
    </li>
  )
}
