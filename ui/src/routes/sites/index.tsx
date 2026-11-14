import { useRef } from 'react'
import { createFileRoute, useNavigate, useRouter } from '@tanstack/react-router'
import { sitesQuery } from '@/features/shell/queries'
import { SitesPage } from '@/features/overview/SitesPage'

// Sites list and the New site dialog (design §3.2, §2.5 `?dialog=`).
export const Route = createFileRoute('/sites/')({
  validateSearch: (search: Record<string, unknown>): { dialog?: 'new-site' } =>
    search.dialog === 'new-site' ? { dialog: 'new-site' } : {},
  staticData: { title: 'Sites' },
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(sitesQuery)
  },
  component: SitesRoute,
})

function SitesRoute() {
  const { dialog } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const router = useRouter()
  // Opening pushes ?dialog=new-site so Back closes it; closing pops that
  // entry, or replaces the URL when the dialog came from a shared link.
  const pushed = useRef(false)
  return (
    <SitesPage
      dialog={dialog === 'new-site'}
      onDialog={(open) => {
        if (open) {
          pushed.current = true
          void navigate({ search: { dialog: 'new-site' } })
        } else if (dialog) {
          if (pushed.current) router.history.back()
          else void navigate({ search: {}, replace: true })
          pushed.current = false
        }
      }}
    />
  )
}
