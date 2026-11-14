// Every route the harness visits at every width. Workstreams add their
// screens here as they land (one line per route; keep paths synthetic).

export type RouteCase = {
  path: string
  /** The page H1 that proves the screen rendered. */
  heading: string | RegExp
  signedIn: boolean
}

export const routes: RouteCase[] = [
  { path: '/next/login', heading: 'Sign in', signedIn: false },
  // Overview is workstream E's; until it lands the site layout renders not-found in the shell.
  { path: '/next/sites/home', heading: /Page not found|Home/, signedIn: true },
  { path: '/next/sites/nowhere/overview', heading: 'Site not found', signedIn: true },
  { path: '/next/does-not-exist', heading: 'Page not found', signedIn: true },
]
