import { queryOptions } from '@tanstack/react-query'
import { api } from './client'
import { isApiError } from './errors'
import { keys } from './keys'
import type { AuthSession } from './types'

// GET /auth/session answers 401 {authenticated:false} when signed out; that
// is an answer, not a failure, so it never triggers the login redirect.

export async function getSession(signal?: AbortSignal): Promise<AuthSession> {
  try {
    const session = await api.get<AuthSession>('/auth/session', { skipAuthRedirect: true, signal })
    return { authenticated: Boolean(session?.authenticated), expires_at: session?.expires_at ?? null }
  } catch (error) {
    if (isApiError(error) && error.status === 401) return { authenticated: false, expires_at: null }
    throw error
  }
}

/** Cached for 60 s (design §2.4): the root route checks it before every screen. */
export const sessionQuery = queryOptions({
  queryKey: keys.session,
  queryFn: ({ signal }) => getSession(signal),
  staleTime: 60_000,
  retry: false,
})

export type ProbeResult = 'authenticated' | 'expired' | 'unknown'

/** Uncached probe used when the SSE stream errors (EventSource cannot see 401s). */
export async function probeSession(): Promise<ProbeResult> {
  try {
    return (await getSession()).authenticated ? 'authenticated' : 'expired'
  } catch {
    return 'unknown'
  }
}
