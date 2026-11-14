import { api } from '@/api/client'
import type { AuthSession } from '@/api/types'

export function login(token: string) {
  return api.post<AuthSession>('/auth/login', { token }, { skipAuthRedirect: true })
}

export function logout() {
  return api.post<AuthSession>('/auth/logout', undefined, { skipAuthRedirect: true })
}
