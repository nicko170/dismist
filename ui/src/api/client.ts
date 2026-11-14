import { ApiError, networkError, toApiError } from './errors'

// request<T>() is the only way the UI talks to dismist (design §7.5): JSON in
// and out, same-origin cookies, ApiError on failure, and any 401 triggers the
// login redirect through the handler the router installs.

export type Query = Record<string, string | number | boolean | null | undefined | readonly (string | number)[]>

export type RequestOptions = {
  body?: unknown
  query?: Query
  /** The object's updated_rev, sent as If-Match: "<rev>" (§4.8 concurrency). */
  ifMatch?: number | string
  signal?: AbortSignal
  headers?: Record<string, string>
  /** Do not redirect to login on 401 (session probes, the login call itself). */
  skipAuthRedirect?: boolean
}

type UnauthorizedHandler = (error: ApiError) => void
let unauthorizedHandler: UnauthorizedHandler | null = null

/** Installs the 401 handler (the router: save drafts, then go to /login?next=). */
export function onUnauthorized(handler: UnauthorizedHandler | null) {
  unauthorizedHandler = handler
}

/** Runs the 401 handler without a request (the SSE probe found the session expired). */
export function notifyUnauthorized() {
  unauthorizedHandler?.(
    new ApiError({ status: 401, code: 'unauthorized', message: 'Your session has ended. Sign in to continue.' }),
  )
}

export function buildUrl(path: string, query?: Query): string {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) value.forEach((v) => params.append(key, String(v)))
    else params.set(key, String(value))
  }
  const search = params.toString()
  return search ? `${path}${path.includes('?') ? '&' : '?'}${search}` : path
}

export async function request<T>(method: string, path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json', ...options.headers }
  let body: BodyInit | undefined
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(options.body)
  }
  if (options.ifMatch !== undefined) headers['If-Match'] = `"${options.ifMatch}"`

  let response: Response
  try {
    response = await fetch(buildUrl(path, options.query), {
      method,
      headers,
      body,
      credentials: 'same-origin',
      cache: 'no-store',
      signal: options.signal,
    })
  } catch (cause) {
    throw networkError(cause)
  }

  if (!response.ok) {
    const error = await toApiError(response)
    if (response.status === 401 && !options.skipAuthRedirect) unauthorizedHandler?.(error)
    throw error
  }
  if (response.status === 204 || response.headers.get('Content-Length') === '0') return undefined as T
  const text = await response.text()
  if (!text) return undefined as T
  try {
    return JSON.parse(text) as T
  } catch {
    throw new ApiError({
      status: response.status,
      code: 'server',
      message: 'dismist sent a response the UI could not read.',
    })
  }
}

export const api = {
  get: <T>(path: string, options?: Omit<RequestOptions, 'body'>) => request<T>('GET', path, options),
  post: <T>(path: string, body?: unknown, options?: RequestOptions) => request<T>('POST', path, { ...options, body }),
  put: <T>(path: string, body?: unknown, options?: RequestOptions) => request<T>('PUT', path, { ...options, body }),
  patch: <T>(path: string, body?: unknown, options?: RequestOptions) => request<T>('PATCH', path, { ...options, body }),
  delete: <T>(path: string, options?: RequestOptions) => request<T>('DELETE', path, options),
}
