import { Check, LayoutGrid } from 'lucide-react'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator } from '@/components/ui/command'
import type { SiteSummary } from '@/api/types'
import { online } from './SiteSwitcher'

type Props = { sites: SiteSummary[]; siteId: string | undefined; onPick: (siteId: string | null) => void }

export default function SiteCommand({ sites, siteId, onPick }: Props) {
  return (
    <Command>
      <CommandInput placeholder="Find a site" aria-label="Find a site" />
      <CommandList>
        <CommandEmpty>No site matches.</CommandEmpty>
        <CommandGroup>
          {sites.map((s) => (
            <CommandItem key={s.id} value={`${s.name} ${s.id}`} onSelect={() => onPick(s.id)}>
              <span className="grid min-w-0 flex-1">
                <span className="truncate">{s.name}</span>
                <span className="truncate text-xs text-muted-foreground">{online(s)}</span>
              </span>
              {s.id === siteId ? <Check aria-label="Current site" className="size-4 text-brand" /> : null}
            </CommandItem>
          ))}
        </CommandGroup>
        <CommandSeparator />
        <CommandGroup>
          <CommandItem value="all sites" onSelect={() => onPick(null)}>
            <LayoutGrid className="size-4 text-muted-foreground" aria-hidden />
            All sites
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </Command>
  )
}
