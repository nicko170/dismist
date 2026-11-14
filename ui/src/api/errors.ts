import type { ErrorBody } from './types'

// The client turns every failure into an ApiError: HTTP errors carry the
// server's {error, code, fields} (design §4.8); a dropped connection becomes
// code "network". Screens show `message` and `code`, never raw JSON or Go
// error strings (§3.0 "Error").

export type ClientErrorCode = ErrorBody['code'] | 'network' | 'server' | 'aborted'

export class ApiError extends Error {
  readonly status: number
  readonly code: ClientErrorCode
  readonly fields: Record<string, string>
  /** Parsed JSON body, if any (409 stale carries the current object). */
  readonly body: unknown
  /** Seconds from Retry-After on 429. */
  readonly retryAfter: number | null

  constructor(init: {
    status: number
    code: ClientErrorCode
    message: string
    fields?: Record<string, string>
    body?: unknown
    retryAfter?: number | null
  }) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.fields = init.fields ?? {}
    this.body = init.body
    this.retryAfter = init.retryAfter ?? null
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError
}

const codeByStatus: Record<number, ClientErrorCode> = {
  400: 'invalid',
  401: 'unauthorized',
  403: 'forbidden_path',
  404: 'not_found',
  409: 'conflict',
  412: 'stale',
  422: 'invalid',
  429: 'rate_limited',
}

const messageByCode: Partial<Record<ClientErrorCode, string>> = {
  unauthorized: 'Your session has ended. Sign in to continue.',
  not_found: 'That item no longer exists.',
  conflict: 'That conflicts with something that already exists.',
  stale: 'This changed since you opened it.',
  rate_limited: 'Too many attempts. Wait a moment and try again.',
  network: 'Could not reach dismist. Check your connection and try again.',
  server: 'dismist hit an internal error. Try again; if it keeps failing, check the controller logs.',
}

function looksLikeErrorBody(value: unknown): value is Partial<ErrorBody> {
  return typeof value === 'object' && value !== null && ('error' in value || 'code' in value || 'fields' in value)
}

function retryAfterSeconds(response: Response): number | null {
  const header = response.headers.get('Retry-After')
  if (!header) return null
  const seconds = Number(header)
  if (Number.isFinite(seconds)) return Math.max(0, Math.ceil(seconds))
  const date = Date.parse(header)
  return Number.isNaN(date) ? null : Math.max(0, Math.ceil((date - Date.now()) / 1000))
}

/** Builds an ApiError from a non-2xx response, tolerating plain-text bodies from older servers. */
export async function toApiError(response: Response): Promise<ApiError> {
  const status = response.status
  const text = await response.text().catch(() => '')
  let body: unknown = undefined
  try {
    body = text ? JSON.parse(text) : undefined
  } catch {
    body = undefined
  }
  const fallbackCode: ClientErrorCode = codeByStatus[status] ?? (status >= 500 ? 'server' : 'invalid')
  if (looksLikeErrorBody(body)) {
    const fields: Record<string, string> = {}
    for (const [key, value] of Object.entries(body.fields ?? {})) {
      if (typeof value === 'string') fields[key] = value
    }
    const code = (typeof body.code === 'string' && body.code) || fallbackCode
    const message =
      (typeof body.error === 'string' && body.error) ||
      Object.values(fields)[0] ||
      messageByCode[code] ||
      `Request failed (${status}).`
    return new ApiError({ status, code, message, fields, body, retryAfter: retryAfterSeconds(response) })
  }
  // Plain text (pre-v1 handlers) or empty: keep a short, single-line message.
  const line = text.split('\n')[0]?.trim() ?? ''
  const usable = line && line.length <= 200 && !line.startsWith('<') ? line : ''
  const sentence = usable ? usable.charAt(0).toUpperCase() + usable.slice(1) : ''
  return new ApiError({
    status,
    code: fallbackCode,
    message: sentence || messageByCode[fallbackCode] || `Request failed (${status}).`,
    body,
    retryAfter: retryAfterSeconds(response),
  })
}

export function networkError(cause: unknown): ApiError {
  if (cause instanceof DOMException && cause.name === 'AbortError') {
    return new ApiError({ status: 0, code: 'aborted', message: 'The request was cancelled.' })
  }
  return new ApiError({ status: 0, code: 'network', message: messageByCode.network! })
}

/** Human title and detail for ErrorState and toasts. */
export function describeError(error: unknown): { message: string; code: string | null } {
  if (isApiError(error)) return { message: error.message, code: error.code }
  if (error instanceof Error && error.message) return { message: error.message, code: null }
  return { message: 'Something went wrong.', code: null }
}

type SetError = (name: never, error: { type: string; message: string }, options?: { shouldFocus?: boolean }) => void

/**
 * Maps a 400/422 `fields` object onto react-hook-form's setError. `map`
 * translates server JSON paths to form field names (identity by default).
 * Returns the messages that matched no field, for the form-level Alert.
 */
export function applyFieldErrors(
  error: unknown,
  setError: SetError,
  formFields: readonly string[],
  map: Record<string, string> = {},
): string[] {
  if (!isApiError(error)) return [describeError(error).message]
  const unmatched: string[] = []
  let first = true
  for (const [path, message] of Object.entries(error.fields)) {
    const name = map[path] ?? path
    if (formFields.includes(name)) {
      setError(name as never, { type: 'server', message }, { shouldFocus: first })
      first = false
    } else {
      unmatched.push(message)
    }
  }
  if (first) {
    // Nothing mapped onto a field: the summary sentence leads the form-level
    // Alert, followed by any field messages it does not already say.
    return [error.message, ...unmatched.filter((m) => m !== error.message)]
  }
  return unmatched
}
