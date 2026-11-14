import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRouter } from '@tanstack/react-router'
import { api } from '@/api/client'
import { keys } from '@/api/keys'
import type { SystemView } from '@/api/types'
import { logout } from '@/features/auth/api'

export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches)
  useEffect(() => {
    const media = window.matchMedia(query)
    const update = () => setMatches(media.matches)
    update()
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [query])
  return matches
}

export function useSignOut() {
  const queryClient = useQueryClient()
  const router = useRouter()
  return useMutation({
    mutationFn: logout,
    onSettled: async () => {
      queryClient.clear()
      queryClient.setQueryData(keys.session, { authenticated: false, expires_at: null })
      await router.navigate({ to: '/login', replace: true })
    },
  })
}

/** Controller version for the sidebar footer; shares the System page's query key. */
export function useVersion(): string | undefined {
  const { data } = useQuery({
    queryKey: keys.system,
    queryFn: ({ signal }) => api.get<SystemView>('/api/system', { signal }),
    staleTime: 5 * 60_000,
    retry: false,
  })
  return data?.version
}
