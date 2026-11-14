import { useQuery } from '@tanstack/react-query'
import { Download, TriangleAlert } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Identifier } from '@/components/app/identifiers'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { CA_BUNDLE_URL, onboardingQuery } from '@/api/queries/onboarding'
import { CodeBlock, CopyTextButton, Panel } from './Panel'

// Card 4 (design §3.15): what the CLI installs, for doing it by hand
// (docs/juniper/AP33.md, appendix).

export function TrustCard() {
  const view = useQuery(onboardingQuery)
  return (
    <Panel
      id="trust"
      title="Trust material (manual path)"
      description="What the CLI writes to the AP, for preparing it by hand over SSH."
    >
      {view.isPending ? (
        <SkeletonRows rows={2} />
      ) : view.isError ? (
        <ErrorState error={view.error} title="Could not load the onboarding details" onRetry={() => void view.refetch()} />
      ) : (
        <div className="grid gap-4">
          <dl className="grid gap-3 text-sm sm:grid-cols-2">
            <div className="grid min-w-0 gap-0.5">
              <dt className="text-xs text-muted-foreground">AP endpoint (WSS URL)</dt>
              <dd className="min-w-0">
                {view.data.dns_mode ? (
                  <span>DNS mode: the AP keeps the stock Mist name and must be pinned.</span>
                ) : view.data.wss_url ? (
                  <Identifier value={view.data.wss_url} />
                ) : (
                  <span className="text-muted-foreground">Not set: start the controller with its public AP URL.</span>
                )}
              </dd>
            </div>
            <div className="grid min-w-0 gap-0.5">
              <dt className="text-xs text-muted-foreground">CA certificates added to the AP’s bundle</dt>
              <dd className="min-w-0 break-words">
                {view.data.ca_pems.length ? view.data.ca_pems.map((ca) => ca.name).join(', ') : 'None listed'}
              </dd>
            </div>
          </dl>
          <div className="flex flex-wrap gap-2">
            <Button asChild variant="outline">
              <a href={CA_BUNDLE_URL} download="cacerts-production.pem">
                <Download /> Download CA bundle
              </a>
            </Button>
          </div>
          <p className="text-sm text-muted-foreground">
            The bundle is the stock Mist CAs plus the roots that issue this controller’s certificate. Install it as{' '}
            <span className="font-mono text-foreground break-all">/rw/etc/mist/ep-control/cacerts-production.pem</span> and this file as{' '}
            <span className="font-mono text-foreground break-all">/rw/etc/mist/ep-control/cloud.ini</span>:
          </p>
          {view.data.cloud_ini ? (
            <div className="grid gap-2">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium">cloud.ini</span>
                <CopyTextButton value={view.data.cloud_ini} label="Copy" />
              </div>
              <CodeBlock label="cloud.ini">{view.data.cloud_ini}</CodeBlock>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No cloud.ini: the controller does not know its public AP URL.</p>
          )}
          <Alert variant="warning" role="note">
            <TriangleAlert />
            <AlertTitle>A factory reset wipes /rw</AlertTitle>
            <AlertDescription>
              The AP then returns to the stock Mist endpoint and bundle and stops connecting here. Keep its backup and run the CLI again.
            </AlertDescription>
          </Alert>
        </div>
      )}
    </Panel>
  )
}
