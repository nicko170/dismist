import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import { onUnauthorized } from '@/api/client'
import { keys } from '@/api/keys'
import { createQueryClient } from '@/api/query-client'
import { saveDrafts } from '@/features/auth/drafts'
import { ThemeProvider } from '@/features/shell/theme'
import { createAppRouter } from './router'
import './styles/index.css'

const queryClient = createQueryClient()
const router = createAppRouter(queryClient)

// Any 401 (design §2.4): keep dirty drafts, forget the session, and go to
// /login?next=<where you were>. Repeated 401s from parallel requests only
// navigate once.
let redirecting = false
onUnauthorized(() => {
  const location = router.state.location
  if (redirecting || location.pathname === '/login') return
  redirecting = true
  saveDrafts()
  queryClient.setQueryData(keys.session, { authenticated: false, expires_at: null })
  void router
    .navigate({ to: '/login', search: { next: location.href }, replace: true })
    .finally(() => {
      redirecting = false
    })
})

async function enableMocks() {
  if (import.meta.env.MODE !== 'mock') return
  const { startMocks } = await import('./api/mocks/browser')
  await startMocks()
}

void enableMocks().then(() => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <ThemeProvider>
        <QueryClientProvider client={queryClient}>
          <TooltipProvider delayDuration={300}>
            <RouterProvider router={router} />
            <Toaster />
          </TooltipProvider>
        </QueryClientProvider>
      </ThemeProvider>
    </StrictMode>,
  )
})
