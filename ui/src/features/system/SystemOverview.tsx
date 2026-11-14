import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { ChainCert, SystemView } from '@/gen/api.gen'
import { systemQuery } from '@/api/queries/system'
import { Chip } from '@/components/app/StatusChip'
import { Identifier, Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { formatBytes, formatDuration } from '@/lib/format'
import { useNow } from '@/lib/time'
import { ThemeSegmented } from '@/features/shell/ThemeControls'
import { Card, Dash, Facts } from '@/features/overview/kit/Card'
import { daysLeft, expiryText, expiryTone } from './cert'

// System overview (design §3.14): controller, AP endpoint and certificate
// expiry (leaf and every CA), database with schema/compat and free space,
// backups, metrics, migration, theme.

export function Expiry({ notAfter }: { notAfter: string }) {
  const now = useNow()
  const days = daysLeft(notAfter, now)
  return <Chip tone={expiryTone(days)}>{expiryText(days)}</Chip>
}

function ChainList({ chain }: { chain: ChainCert[] }) {
  if (!chain.length) return <Dash />
  return (
    <ul className="grid gap-1.5">
      {chain.map((ca) => (
        <li key={`${ca.subject}-${ca.not_after}`} className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="min-w-0 break-words">{ca.subject}</span>
          <span className="text-xs text-muted-foreground">{ca.role}</span>
          <Expiry notAfter={ca.not_after} />
        </li>
      ))}
    </ul>
  )
}

export const migrationText: Record<SystemView['migration']['state'], string> = {
  not_run: 'Not run',
  committed: 'Committed',
  legacy: 'Legacy mode',
}

export function SystemOverview() {
  const query = useQuery(systemQuery)
  const now = useNow()
  if (query.isPending) return <SkeletonRows rows={5} />
  if (query.isError) return <ErrorState error={query.error} title="Could not load the system status" onRetry={() => void query.refetch()} />
  const s = query.data
  const uptime = s.started_at ? Math.max(0, (now - Date.parse(s.started_at)) / 1000) : null
  // A migration step needs the DB size plus 64 MiB free (design §4.1).
  const lowSpace = s.db.free_bytes > 0 && s.db.free_bytes < s.db.size_bytes + 64 * 1024 ** 2
  return (
    <div className="grid min-w-0 gap-4 lg:grid-cols-2">
      <Card title="Controller">
        <Facts
          items={[
            { label: 'Version', value: s.version || <Dash /> },
            { label: 'Commit', value: s.commit ? <Mono>{s.commit}</Mono> : <Dash /> },
            { label: 'Go', value: s.go || <Dash /> },
            { label: 'Uptime', value: uptime !== null ? formatDuration(uptime) : <Dash /> },
            { label: 'Readiness', value: s.ready ? <Chip tone="good">Ready</Chip> : <Chip tone="crit">Not ready</Chip> },
          ]}
        />
      </Card>

      <Card title="AP endpoint">
        <Facts
          items={[
            { label: 'WSS URL', value: s.wss.url ? <Identifier value={s.wss.url} /> : <span className="text-muted-foreground">Not set (-ap-url)</span> },
            { label: 'Listening on', value: s.wss.listen ? <Mono>{s.wss.listen}</Mono> : <Dash /> },
            { label: 'Connected APs', value: s.wss.sessions },
            {
              label: 'Certificate',
              value: s.wss.cert?.subject ? (
                <span className="grid gap-1">
                  <span className="break-words">{s.wss.cert.subject}</span>
                  {s.wss.cert.sans?.length ? <span className="text-xs break-words text-muted-foreground">SANs: {s.wss.cert.sans.join(', ')}</span> : null}
                  <span className="text-xs text-muted-foreground">Issuer: {s.wss.cert.issuer || '–'}</span>
                  <Expiry notAfter={s.wss.cert.not_after} />
                </span>
              ) : (
                <Dash />
              ),
            },
            { label: 'CA chain and bundle', value: <ChainList chain={s.wss.chain ?? []} /> },
            { label: 'DNS mode', value: s.dns_mode ? 'On (pinned IPs required)' : 'Off' },
            { label: 'Trusted proxies', value: s.trusted_proxies?.length ? <Mono>{s.trusted_proxies.join(', ')}</Mono> : 'None' },
          ]}
        />
        <p className="mt-3 text-xs text-muted-foreground">An AP that trusts only an expiring CA stops connecting when that CA lapses, even with a fresh leaf.</p>
      </Card>

      <Card title="Database">
        <Facts
          items={[
            { label: 'Path', value: <Mono className="text-xs">{s.db.path}</Mono> },
            { label: 'Size', value: formatBytes(s.db.size_bytes) },
            {
              label: 'Free space',
              value: (
                <span className={lowSpace ? 'font-medium text-warning' : undefined}>
                  {formatBytes(s.db.free_bytes)}
                  {lowSpace ? ' (too little for a migration copy)' : ''}
                </span>
              ),
            },
            { label: 'Schema · compat', value: `${s.db.schema} · ${s.db.compat}` },
            {
              label: 'Rollback to pre-v2 releases',
              value: s.db.rollback_without_restore ? (
                <Chip tone="good">Possible without a restore</Chip>
              ) : (
                <Chip tone="warn">Needs a database restore first</Chip>
              ),
            },
            {
              label: 'Retention',
              value: Object.keys(s.db.retention ?? {}).length ? (
                Object.entries(s.db.retention)
                  .map(([k, v]) => `${k} ${v}`)
                  .join(' · ')
              ) : (
                <Dash />
              ),
            },
          ]}
        />
      </Card>

      <Card
        title="Backups"
        action={
          <Link to="/system/backups" className="text-sm text-brand hover:underline">
            Manage
          </Link>
        }
      >
        <Facts
          items={[
            { label: 'Last snapshot', value: <RelativeTime value={s.backup.last_success} fallback="None yet" /> },
            { label: 'Next', value: s.backup.next ? <RelativeTime value={s.backup.next} /> : 'Snapshots are off' },
            { label: 'Kept', value: `${s.backup.count ?? '–'} of ${s.backup.keep} · ${formatBytes(s.backup.used_bytes)} of ${formatBytes(s.backup.max_bytes)}` },
            { label: 'Last problem', value: <span className="text-warning">{s.backup.last_error}</span>, hidden: !s.backup.last_error },
          ]}
        />
      </Card>

      <Card title="Metrics">
        <Facts
          items={[
            { label: 'Listener', value: s.metrics.listen ? <Mono>{s.metrics.listen}</Mono> : 'Off' },
            { label: 'Remote scrapes', value: s.metrics.remote ? 'Allowed' : 'Loopback only' },
            { label: 'Names on info series', value: s.metrics.names ? 'On' : 'Off' },
            { label: 'Client metrics', value: { off: 'Off', aggregate: 'Aggregate', 'per-client': 'Per client' }[s.metrics.clients] ?? s.metrics.clients },
          ]}
        />
      </Card>

      <Card
        title="Migration"
        action={
          <Link to="/system/migration" className="text-sm text-brand hover:underline">
            Details
          </Link>
        }
      >
        <Facts
          items={[
            {
              label: 'Schema v2',
              value: <Chip tone={s.migration.state === 'committed' ? 'good' : s.migration.state === 'legacy' ? 'warn' : 'idle'}>{migrationText[s.migration.state] ?? s.migration.state}</Chip>,
            },
            { label: 'Reason', value: s.migration.reason, hidden: !s.migration.reason },
          ]}
        />
      </Card>

      <Card title="Theme">
        <ThemeSegmented />
      </Card>
    </div>
  )
}
