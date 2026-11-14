// `next` after sign-in is accepted only if it is a same-app path: it starts
// with "/", not "//" (or "/\", which browsers treat the same), and is not the
// login page itself (design §2.4). Anything else goes to "/".

export function safeNext(next: unknown): string {
  if (typeof next !== 'string' || !next.startsWith('/')) return '/'
  if (next.startsWith('//') || next.startsWith('/\\')) return '/'
  for (let i = 0; i < next.length; i++) if (next.charCodeAt(i) < 0x20) return '/'
  const path = next.split(/[?#]/)[0]
  if (path === '/login' || path.startsWith('/login/')) return '/'
  return next
}
