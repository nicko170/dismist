import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ClipboardPen } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { PageHeader } from '@/components/app/PageHeader'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { sitesQuery } from '@/features/shell/queries'
import { CliCard } from './CliCard'
import { PendingCard } from './PendingCard'
import { Panel } from './Panel'
import { TrustCard } from './TrustCard'

// The onboarding hub (design §3.15): waiting APs, the CLI command builder,
// manual registration and the trust material, on one page.

export function OnboardHub({ siteId, highlight }: { siteId?: string; highlight?: string }) {
  const sites = useQuery(sitesQuery)
  const controller = typeof window === 'undefined' ? 'https://dismist.example.net' : window.location.origin
  return (
    <div className="mx-auto grid w-full max-w-5xl gap-5">
      <PageHeader title="Add an AP" meta="Bring an AP33 onto this controller. A second AP behind the same NAT needs nothing special." />
      <PendingCard siteId={siteId} highlight={highlight} />
      {sites.isPending ? (
        <SkeletonRows rows={2} />
      ) : sites.isError ? (
        <ErrorState error={sites.error} title="Could not load sites" onRetry={() => void sites.refetch()} />
      ) : (
        <CliCard sites={sites.data} siteId={siteId} controller={controller} />
      )}
      <Panel
        id="manual"
        title="Register manually"
        description="Already have the AP’s ID and its 32-byte secret (from its EEPROM backup)? Register it here, then watch it connect."
      >
        <div>
          <Button asChild variant="outline">
            <Link to="/onboard/register" search={{ site: siteId }}>
              <ClipboardPen /> Register an AP
            </Link>
          </Button>
        </div>
      </Panel>
      <TrustCard />
    </div>
  )
}
