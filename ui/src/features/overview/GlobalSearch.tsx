import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Dialog } from 'radix-ui'
import { MapPin, RadioTower, Search, Smartphone } from 'lucide-react'
import { allAPsQuery } from '@/api/queries/aps'
import { siteClientsQuery } from '@/api/queries/clients'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { sitesQuery } from '@/features/shell/queries'
import { displayName } from '@/features/clients/model'

// Global search (design §8.7, phase 4): sites, every AP in every site (name,
// ID, LAN IP, public address) and the current site's live clients (name,
// MAC, IP). Ctrl/⌘ K opens it. Proposed for the top bar (WS-D); until then
// the site overview carries the button.

export function GlobalSearch({ siteId }: { siteId?: string }) {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const sites = useQuery({ ...sitesQuery, enabled: open })
  const aps = useQuery({ ...allAPsQuery, enabled: open })
  const clients = useQuery({ ...siteClientsQuery(siteId ?? ''), enabled: open && Boolean(siteId), refetchInterval: false })
  const siteName = new Map((sites.data ?? []).map((s) => [s.id, s.name]))

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
              <CommandInput placeholder="Sites, APs, clients, MAC or IP" autoCapitalize="none" autoCorrect="off" spellCheck={false} />
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
                        value={`ap ${ap.name} ${ap.id} ${ap.id.replace(/-/g, ':')} ${ap.lan_ip} ${ap.remote_address}`}
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
              </CommandList>
            </Command>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  )
}
