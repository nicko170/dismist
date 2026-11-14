import { useDeferredValue, useEffect, useState } from 'react'
import { queryOptions, useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Dialog } from 'radix-ui'
import { History, MapPin, RadioTower, Search, Smartphone } from 'lucide-react'
import { api } from '@/api/client'
import type { SearchHit, SearchResult } from '@/gen/api.gen'
import { allAPsQuery } from '@/api/queries/aps'
import { siteClientsQuery } from '@/api/queries/clients'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { sitesQuery } from '@/features/shell/queries'
import { displayName } from '@/features/clients/model'

// Global search (design §8.7, phase 4): sites, every AP in every site (name,
// ID, LAN IP, public address) and the current site's live clients (name,
// MAC, IP), filtered here; plus what only the server can find
// (GET /api/search): offline clients by alias or recent session, and APs by
// serial. Ctrl/⌘ K opens it. Proposed for the top bar (WS-D); until then
// the site overview carries the button.

const searchQuery = (q: string) =>
  queryOptions({
    queryKey: ['search', q] as const,
    queryFn: ({ signal }) => api.get<SearchResult>('/api/search', { query: { q }, signal }),
    staleTime: 30_000,
  })

/** The server's hits the local lists do not already show for this text. */
export function extraHits(hits: SearchHit[], text: string, shown: { clients: Set<string>; apValues: Map<string, string> }): SearchHit[] {
  const needle = text.trim().toLowerCase()
  return hits.filter((hit) => {
    if (hit.kind === 'client') return !shown.clients.has(hit.id)
    if (hit.kind === 'ap') return !(shown.apValues.get(hit.id) ?? '').toLowerCase().includes(needle)
    return false
  })
}

export function GlobalSearch({ siteId }: { siteId?: string }) {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const sites = useQuery({ ...sitesQuery, enabled: open })
  const aps = useQuery({ ...allAPsQuery, enabled: open })
  const clients = useQuery({ ...siteClientsQuery(siteId ?? ''), enabled: open && Boolean(siteId), refetchInterval: false })
  const siteName = new Map((sites.data ?? []).map((s) => [s.id, s.name]))
  const [text, setText] = useState('')
  const deferred = useDeferredValue(text.trim())
  const server = useQuery({ ...searchQuery(deferred), enabled: open && deferred.length >= 2, retry: false })
  const apValue = (ap: { name: string; id: string; lan_ip: string; remote_address: string }) =>
    `ap ${ap.name} ${ap.id} ${ap.id.replace(/-/g, ':')} ${ap.lan_ip} ${ap.remote_address}`
  const more =
    deferred.length >= 2 && server.data
      ? extraHits(server.data.hits, deferred, {
          clients: new Set((siteId ? (clients.data ?? []) : []).map((c) => c.mac)),
          apValues: new Map((aps.data ?? []).map((ap) => [ap.id, apValue(ap)])),
        })
      : []

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setOpen((o) => !o)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const go = (to: string, params?: Record<string, string>) => {
    setOpen(false)
    void navigate({ to: to as never, params: params as never })
  }

  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)} aria-keyshortcuts="Control+K Meta+K">
        <Search />
        Search
        <kbd aria-hidden className="ml-1 rounded border px-1 font-mono text-xs text-muted-foreground max-md:hidden">
          ⌘K
        </kbd>
      </Button>
      <Dialog.Root open={open} onOpenChange={setOpen}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-50 bg-foreground/30 backdrop-blur-[2px] dark:bg-background/60" />
          <Dialog.Content className="fixed inset-x-0 top-0 z-50 mx-auto w-full max-w-xl overflow-hidden border bg-popover shadow-xl outline-none max-md:h-dvh md:top-[12vh] md:rounded-xl">
            <Dialog.Title className="sr-only">Search</Dialog.Title>
            <Dialog.Description className="sr-only">Find a site, access point or client.</Dialog.Description>
            <Command loop>
              <CommandInput
                placeholder="Sites, APs, clients, MAC or IP"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                value={text}
                onValueChange={setText}
              />
              <CommandList className="max-h-[min(60vh,28rem)] max-md:max-h-[calc(100dvh-4rem)]">
                <CommandEmpty>No matches.</CommandEmpty>
                {sites.data?.length ? (
                  <CommandGroup heading="Sites">
                    {sites.data.map((s) => (
                      <CommandItem key={s.id} value={`site ${s.name} ${s.id}`} onSelect={() => go('/sites/$siteId/overview', { siteId: s.id })}>
                        <MapPin aria-hidden />
                        {s.name}
                      </CommandItem>
                    ))}
                  </CommandGroup>
                ) : null}
                {aps.data?.length ? (
                  <CommandGroup heading="Access points">
                    {aps.data.map((ap) => (
                      <CommandItem
                        key={ap.id}
                        value={apValue(ap)}
                        onSelect={() => (ap.site_id ? go('/sites/$siteId/aps/$apId', { siteId: ap.site_id, apId: ap.id }) : go('/aps/$apId', { apId: ap.id }))}
                      >
                        <RadioTower aria-hidden />
                        <span className="min-w-0 flex-1 truncate">{ap.name}</span>
                        <span className="text-xs text-muted-foreground">{siteName.get(ap.site_id) ?? ap.lan_ip}</span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                ) : null}
                {clients.data?.length && siteId ? (
                  <CommandGroup heading="Clients">
                    {clients.data.map((c) => (
                      <CommandItem
                        key={c.mac}
                        value={`client ${displayName(c)} ${c.mac} ${c.mac.replace(/-/g, ':')} ${c.ip}`}
                        onSelect={() => go('/sites/$siteId/clients/$mac', { siteId, mac: c.mac })}
                      >
                        <Smartphone aria-hidden />
                        <span className="min-w-0 flex-1 truncate">{displayName(c)}</span>
                        <span className="text-xs text-muted-foreground">{c.ip || c.mac}</span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                ) : null}
                {more.length ? (
                  <CommandGroup heading="Offline clients and AP serials">
                    {more.map((hit) => (
                      <CommandItem
                        key={`${hit.kind}-${hit.id}`}
                        // Always matches the typed text: the server matched it.
                        value={`${hit.kind} ${deferred} ${hit.title} ${hit.id}`}
                        onSelect={() =>
                          hit.kind === 'ap'
                            ? hit.site_id
                              ? go('/sites/$siteId/aps/$apId', { siteId: hit.site_id, apId: hit.id })
                              : go('/aps/$apId', { apId: hit.id })
                            : hit.site_id
                              ? go('/sites/$siteId/clients/$mac', { siteId: hit.site_id, mac: hit.id })
                              : go('/clients/$mac', { mac: hit.id })
                        }
                      >
                        {hit.kind === 'ap' ? <RadioTower aria-hidden /> : <History aria-hidden />}
                        <span className="min-w-0 flex-1 truncate">{hit.title}</span>
                        <span className="text-xs text-muted-foreground">{hit.detail}</span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                ) : null}
              </CommandList>
            </Command>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  )
}
