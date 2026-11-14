import { useId, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ChevronDown, KeyRound, Terminal } from 'lucide-react'
import { Chip } from '@/components/app/StatusChip'
import { Button } from '@/components/ui/button'
import { Input, inputClass } from '@/components/ui/input'
import { adminTokensQuery } from '@/api/queries/onboarding'
import type { SiteSummary } from '@/gen/api.gen'
import { cn } from '@/lib/utils'
import { buildOnboardCommand, parseAPAddress } from './command'
import { CodeBlock, CopyTextButton, FieldRow, Panel, describedBy } from './Panel'

// Card 2 (design §3.15): the recommended path. The CLI runs on a laptop on
// the AP's LAN, because the controller (in a cluster) cannot reach it.

export function CliCard({ sites, siteId, controller }: { sites: SiteSummary[]; siteId?: string; controller: string }) {
  const ids = { site: useId(), name: useId(), address: useId() }
  const [site, setSite] = useState(() => sites.find((s) => s.id === siteId)?.id ?? sites.find((s) => s.name === 'Home')?.id ?? sites[0]?.id ?? '')
  const [name, setName] = useState('')
  const [address, setAddress] = useState('')
  const [touched, setTouched] = useState(false)
  const tokens = useQuery(adminTokensQuery)

  const siteName = sites.find((s) => s.id === site)?.name ?? site
  const command = buildOnboardCommand({ address: address || '192.0.2.10', controller, site: siteName, name: name || 'Hallway' })
  const addressError = touched && address && !parseAPAddress(address) ? 'Enter the AP’s LAN IP address or host name, optionally as user@host.' : undefined
  const addressHelp = 'The address your laptop reaches the AP at over SSH (root login).'

  return (
    <Panel
      id="cli"
      title="Prepare an AP with the CLI"
      badge={<Chip tone="good">Recommended</Chip>}
      description={
        <>
          From a laptop on the AP’s LAN, <span className="font-mono text-foreground">dismist ap33 onboard</span> backs up the AP, reads its
          secret, installs this controller’s endpoint and CA bundle, registers it here, reboots it and waits until it is in sync.
        </>
      }
    >
      <div className="grid gap-4 md:grid-cols-3">
        <FieldRow id={ids.site} label="Site">
          <div className="relative">
            <select id={ids.site} className={cn(inputClass, 'appearance-none pr-10')} value={site} onChange={(event) => setSite(event.target.value)}>
              {sites.length === 0 ? <option value="">No sites yet</option> : null}
              {sites.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
            <ChevronDown aria-hidden className="pointer-events-none absolute top-1/2 right-3 size-4 -translate-y-1/2 text-muted-foreground" />
          </div>
        </FieldRow>
        <FieldRow id={ids.name} label="AP name">
          <Input id={ids.name} value={name} placeholder="Hallway" maxLength={64} onChange={(event) => setName(event.target.value)} />
        </FieldRow>
        <FieldRow id={ids.address} label="AP LAN address" help={addressHelp} error={addressError}>
          <Input
            id={ids.address}
            value={address}
            placeholder="192.0.2.10"
            inputMode="url"
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            aria-invalid={addressError ? true : undefined}
            aria-describedby={describedBy(ids.address, addressError, addressHelp)}
            onBlur={() => setTouched(true)}
            onChange={(event) => setAddress(event.target.value)}
          />
        </FieldRow>
      </div>

      {command ? (
        <div className="grid gap-2">
          <div className="flex items-center justify-between gap-2">
            <span className="flex items-center gap-2 text-sm font-medium">
              <Terminal aria-hidden className="size-4 text-muted-foreground" /> Command
            </span>
            <CopyTextButton value={command} label="Copy" />
          </div>
          <CodeBlock label="Onboard command">{command}</CodeBlock>
        </div>
      ) : null}

      <ul className="grid gap-1.5 text-sm text-muted-foreground">
        <li>
          The token file holds an API token with admin scope (one line, mode 600). It never goes on the command line.
        </li>
        <li>Add <span className="font-mono text-foreground">--dry-run</span> to see every step without changing anything.</li>
        <li>
          Backups (the AP’s <span className="font-mono text-foreground">/rw</span> and its EEPROM) go to{' '}
          <span className="font-mono text-foreground">~/.local/share/dismist/backups</span>. They hold the AP’s secret: keep them private.
        </li>
      </ul>

      {tokens.isSuccess && tokens.data.length === 0 ? (
        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-dashed p-3 text-sm">
          <KeyRound aria-hidden className="size-4 text-muted-foreground" />
          <span className="min-w-0 flex-1">There is no API token with admin scope yet. Create one for the CLI.</span>
          <Button asChild variant="outline" size="sm">
            <Link to={'/system/tokens' as never}>Create an API token</Link>
          </Button>
        </div>
      ) : null}
    </Panel>
  )
}
