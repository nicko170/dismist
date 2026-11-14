import {
  Activity,
  AudioWaveform,
  CirclePlus,
  Gauge,
  History,
  RadioTower,
  Server,
  SlidersHorizontal,
  Smartphone,
  Wifi,
  type LucideIcon,
} from 'lucide-react'

// One navigation model for the sidebar, the icon rail, the bottom tabs and
// the More sheet (design §2.2). Paths are relative to the router basepath.

export type NavId =
  | 'overview'
  | 'aps'
  | 'clients'
  | 'wlans'
  | 'rf'
  | 'events'
  | 'changes'
  | 'settings'
  | 'onboard'
  | 'system'

export type NavItem = {
  id: NavId
  label: string
  /** Shorter label for the bottom tabs. */
  tab?: string
  icon: LucideIcon
  /** Site-scoped items need a site; global ones do not. */
  link: (siteId: string | undefined) => NavTarget
  site: boolean
  /** Where it lives on phones. */
  phone: 'tab' | 'more'
}

export type NavTarget = { to: string; search?: Record<string, string> }

const sitePath =
  (section: string) =>
  (siteId: string | undefined): NavTarget => ({ to: siteId ? `/sites/${encodeURIComponent(siteId)}/${section}` : '/sites' })

export const siteNav: NavItem[] = [
  { id: 'overview', label: 'Overview', icon: Gauge, link: sitePath('overview'), site: true, phone: 'tab' },
  { id: 'aps', label: 'Access points', tab: 'APs', icon: RadioTower, link: sitePath('aps'), site: true, phone: 'tab' },
  { id: 'clients', label: 'Clients', icon: Smartphone, link: sitePath('clients'), site: true, phone: 'tab' },
  { id: 'wlans', label: 'WLANs', icon: Wifi, link: sitePath('wlans'), site: true, phone: 'tab' },
  { id: 'rf', label: 'Radio', icon: AudioWaveform, link: sitePath('rf'), site: true, phone: 'more' },
  { id: 'events', label: 'Events', icon: Activity, link: sitePath('events'), site: true, phone: 'more' },
  { id: 'changes', label: 'Changes', icon: History, link: sitePath('changes'), site: true, phone: 'more' },
  { id: 'settings', label: 'Settings', icon: SlidersHorizontal, link: sitePath('settings'), site: true, phone: 'more' },
]

export const globalNav: NavItem[] = [
  {
    id: 'onboard',
    label: 'Add an AP',
    icon: CirclePlus,
    link: (siteId) => ({ to: '/onboard', search: siteId ? { site: siteId } : undefined }),
    site: false,
    phone: 'more',
  },
  { id: 'system', label: 'System', icon: Server, link: () => ({ to: '/system' }), site: false, phone: 'more' },
]

/** Which item a pathname belongs to (for aria-current on every nav surface). */
export function activeNavId(pathname: string): NavId | null {
  const site = pathname.match(/^\/sites\/[^/]+\/([^/?#]+)/)
  if (site) {
    const section = site[1]
    return (siteNav.find((item) => item.id === section)?.id ?? null) as NavId | null
  }
  if (pathname.startsWith('/onboard')) return 'onboard'
  if (pathname.startsWith('/system')) return 'system'
  return null
}
