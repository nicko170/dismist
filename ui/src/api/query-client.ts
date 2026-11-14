import { QueryClient } from '@tanstack/react-query'
import { isApiError } from './errors'
import { stale } from './keys'

// Defaults (design §7.5): config objects stay fresh 30 s and the stream
// invalidates them; live views opt in to shorter staleTime and focus refetch.
// 4xx answers are not retried: they will not change on their own.
export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: stale.config,
        refetchOnWindowFocus: false,
        retry: (failures, error) => {
          if (isApiError(error) && error.status >= 400 && error.status < 500) return false
          return failures < 2
        },
      },
      mutations: { retry: false },
    },
  })
}
