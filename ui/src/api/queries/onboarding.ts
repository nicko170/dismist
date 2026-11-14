import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { APDetail, APInput, APView, MutationResponse, OnboardingView, PendingAP, TokenView } from '@/gen/api.gen'

// Onboarding reads and writes (design §3.15, §4.9). Owned by workstream H.

export const onboardingKeys = {
  view: ['onboarding'] as const,
  /** Every registered AP, across sites (GET /api/aps): the Register form refuses duplicates. */
  allAPs: ['aps'] as const,
  /** Admin-scope token count for the CLI card ("Create an API token" when there is none). */
  tokens: ['onboarding', 'tokens'] as const,
  pending: (dismissed: boolean) => (dismissed ? ([...keys.pending, 'dismissed'] as const) : keys.pending),
}

export const onboardingQuery = queryOptions({
  queryKey: onboardingKeys.view,
  queryFn: ({ signal }) => api.get<OnboardingView>('/api/onboarding', { signal }),
  staleTime: stale.config,
})

/** GET /api/aps/pending (?dismissed=1 lists the dismissed ones). Polled: the hub has no site stream. */
export function pendingQuery(dismissed = false) {
  return queryOptions({
    queryKey: onboardingKeys.pending(dismissed),
    queryFn: ({ signal }) =>
      api.get<PendingAP[] | null>('/api/aps/pending', { signal, query: dismissed ? { dismissed: 1 } : undefined }).then((rows) => rows ?? []),
    staleTime: stale.live,
    refetchInterval: 10_000,
  })
}

export const allAPsQuery = queryOptions({
  queryKey: onboardingKeys.allAPs,
  queryFn: ({ signal }) => api.get<APView[] | null>('/api/aps', { signal }).then((rows) => rows ?? []),
  staleTime: stale.config,
})

/** Accepts the v1 {tokens: [...]} and a bare array. */
export function normaliseTokens(body: unknown): TokenView[] {
  if (Array.isArray(body)) return body as TokenView[]
  const tokens = (body as { tokens?: unknown } | null)?.tokens
  return Array.isArray(tokens) ? (tokens as TokenView[]) : []
}

export const adminTokensQuery = queryOptions({
  queryKey: onboardingKeys.tokens,
  queryFn: async ({ signal }) => normaliseTokens(await api.get<unknown>('/api/system/tokens', { signal })).filter((t) => t.scope === 'admin'),
  staleTime: stale.config,
  retry: false,
})

/** GET /api/aps/$id, shared with the AP views (the site stream patches this key). */
export function apDetailQuery(apId: string) {
  return queryOptions({
    queryKey: keys.ap(apId),
    queryFn: ({ signal }) => api.get<APDetail>(`/api/aps/${encodeURIComponent(apId)}`, { signal }),
    staleTime: stale.live,
    retry: (count, error) => count < 2 && !(error as { status?: number }).status,
  })
}

export function useRegisterAP() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: APInput) => api.post<MutationResponse<APView>>('/api/aps', input),
    onSuccess: (response, input) => {
      void queryClient.invalidateQueries({ queryKey: onboardingKeys.allAPs })
      void queryClient.invalidateQueries({ queryKey: keys.pending })
      void queryClient.invalidateQueries({ queryKey: keys.sites })
      if (input.site_id) void queryClient.invalidateQueries({ queryKey: keys.siteAPs(input.site_id) })
      if (response?.object) queryClient.removeQueries({ queryKey: keys.ap(input.id), exact: true })
    },
  })
}

/** DELETE /api/aps/pending/$id?days=30: hidden for 30 days unless "Show dismissed" is on. */
export function useDismissPending() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.delete<void>(`/api/aps/pending/${encodeURIComponent(id)}`, { query: { days: 30 } }),
    onMutate: async (id) => {
      await queryClient.cancelQueries({ queryKey: keys.pending, exact: true })
      const previous = queryClient.getQueryData<PendingAP[]>(keys.pending)
      if (previous) queryClient.setQueryData(keys.pending, previous.filter((row) => row.id !== id))
      return { previous }
    },
    onError: (_error, _id, context) => {
      if (context?.previous) queryClient.setQueryData(keys.pending, context.previous)
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: keys.pending }),
  })
}

/** The CA bundle download (GET /api/onboarding/ca-bundle.pem). */
export const CA_BUNDLE_URL = '/api/onboarding/ca-bundle.pem'
