import { Link } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft } from 'lucide-react'
import { keys } from '@/api/keys'
import { networkExpertQuery, networkQuery, putNetworkExpert } from '@/api/queries/networks'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { buttonVariants } from '@/components/ui/button'
import { usePageTitle } from '@/features/shell/title'
import { ExpertPatchEditor } from '@/features/settings/kit/ExpertPatchEditor'
import { UnsavedChangesGuard } from '@/features/settings/kit/guard'
import { LegacyModeBanner, useTypedEditing } from '@/features/settings/kit/site'

// The WLAN expert patch (design §3.9 "Expert"): a JSON merge patch onto this
// WLAN object only.

export function WlanExpert({ siteId, wlanId }: { siteId: string; wlanId: string }) {
  const network = useQuery(networkQuery(siteId, wlanId))
  const expert = useQuery(networkExpertQuery(siteId, wlanId))
  const queryClient = useQueryClient()
  const { typed } = useTypedEditing()
  const name = network.data?.ssid ?? wlanId
  usePageTitle(`Expert patch · ${name}`)
  return (
    <div className="grid gap-5">
      <Link to={`/sites/${encodeURIComponent(siteId)}/wlans/${encodeURIComponent(wlanId)}` as never} className={buttonVariants({ variant: 'ghost', size: 'sm', className: 'justify-self-start' })}>
        <ArrowLeft /> Back to {name}
      </Link>
      <LegacyModeBanner />
      {expert.isError ? (
        <>
          <h1 tabIndex={-1} data-page-title className="text-2xl font-semibold">Expert patch · WLAN {name}</h1>
          <ErrorState error={expert.error} title="Could not load the expert patch" onRetry={() => void expert.refetch()} />
        </>
      ) : !expert.data ? (
        <>
          <h1 tabIndex={-1} data-page-title className="text-2xl font-semibold">Expert patch · WLAN {name}</h1>
          <SkeletonRows rows={3} />
        </>
      ) : (
        <ExpertPatchEditor
          scope="wlan"
          heading={`Expert patch · WLAN ${name}`}
          headingLevel="h1"
          view={expert.data}
          siteId={siteId}
          legacy={!typed}
          save={(patch, options) => putNetworkExpert(siteId, wlanId, { patch }, options)}
          onSaved={(response) => {
            queryClient.setQueryData(networkExpertQuery(siteId, wlanId).queryKey, response.object)
            void queryClient.invalidateQueries({ queryKey: keys.network(wlanId) })
          }}
        />
      )}
      <UnsavedChangesGuard />
    </div>
  )
}
