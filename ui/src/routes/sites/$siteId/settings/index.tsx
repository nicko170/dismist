import { createFileRoute, redirect } from '@tanstack/react-router'

// /sites/$siteId/settings → settings/general (design §2.3).
export const Route = createFileRoute('/sites/$siteId/settings/')({
  beforeLoad: ({ params }) => {
    throw redirect({ to: `/sites/${encodeURIComponent(params.siteId)}/settings/general` as never, replace: true })
  },
})
