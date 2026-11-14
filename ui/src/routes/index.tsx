import { createFileRoute, redirect } from '@tanstack/react-router'
import { forgetLastSite, readLastSite } from '@/features/shell/site'
import { sitesQuery } from '@/features/shell/queries'

// `/` (design §2.3): the last-used site's overview if it still exists, else
// /sites when any site exists, else /onboard.
export const Route = createFileRoute('/')({
  beforeLoad: async ({ context }) => {
    const sites = await context.queryClient.ensureQueryData(sitesQuery)
    const last = readLastSite()
    if (last && sites.some((site) => site.id === last)) {
      throw redirect({ to: `/sites/${encodeURIComponent(last)}/overview` as never, replace: true })
    }
    if (last) forgetLastSite(last)
    throw redirect({ to: (sites.length ? '/sites' : '/onboard') as never, replace: true })
  },
})
