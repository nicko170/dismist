import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Download, TriangleAlert } from 'lucide-react'
import { toast } from 'sonner'
import { downloadBackup, systemQuery } from '@/api/queries/system'
import { describeError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { formatBytes } from '@/lib/format'
import { Card, Dash, Facts } from '@/features/overview/kit/Card'
import { ConfirmDialog } from '@/features/overview/kit/ConfirmDialog'

// Backups (design §3.14, §4.1): download a consistent copy now (audit
// logged; the file holds PSKs and AP secrets), scheduled snapshots, and the
// pre-migration copies listed separately.

export function BackupsPage() {
  const query = useQuery(systemQuery)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const download = async () => {
    setBusy(true)
    setError(null)
    try {
      const name = await downloadBackup()
      toast.success('Backup downloaded', { description: name })
      setConfirm(false)
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(false)
    }
  }

  if (query.isPending) return <SkeletonRows rows={3} />
  if (query.isError) return <ErrorState error={query.error} title="Could not load backup status" onRetry={() => void query.refetch()} />
  const b = query.data.backup
  return (
    <div className="grid min-w-0 gap-4 lg:grid-cols-2">
      <Card title="Download a backup" className="lg:col-span-2">
        <div className="grid gap-3">
          <p className="text-sm">A consistent copy of the controller database, taken now. Restore it by putting it back as controller.db while dismist is stopped.</p>
          <p className="flex items-start gap-2 rounded-md border border-status-warn/45 bg-status-warn/10 px-3 py-2 text-sm">
            <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warning" />
            The file contains every WLAN passphrase and AP secret. Store it like a password.
          </p>
          <Button variant="brand" className="justify-self-start" onClick={() => setConfirm(true)}>
            <Download />
            Download backup now
          </Button>
        </div>
      </Card>
      <Card title="Scheduled snapshots">
        <Facts
          items={[
            { label: 'Last success', value: <RelativeTime value={b.last_success} fallback="None yet" /> },
            { label: 'Next run', value: b.next ? <RelativeTime value={b.next} /> : 'Snapshots are off' },
            { label: 'Interval', value: b.interval || <Dash /> },
            { label: 'Kept', value: `${b.count ?? '–'} (keep ${b.keep})` },
            { label: 'Space used', value: `${formatBytes(b.used_bytes)} of ${formatBytes(b.max_bytes)}` },
            { label: 'Directory', value: b.dir ? <Mono className="text-xs">{b.dir}</Mono> : <Dash /> },
            { label: 'Last problem', value: <span className="text-warning">{b.last_error}</span>, hidden: !b.last_error },
          ]}
        />
      </Card>
      <Card title="Pre-migration copies" description="Written before a schema migration; kept until the migration is finalised.">
        {b.pre_migration?.length ? (
          <ul className="grid gap-1.5 text-sm">
            {b.pre_migration.map((copy) => (
              <li key={copy.name} className="flex min-w-0 flex-wrap justify-between gap-2">
                <Mono className="text-xs">{copy.name}</Mono>
                <span className="text-muted-foreground tabular-nums">{formatBytes(copy.size_bytes)}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">None.</p>
        )}
      </Card>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title="Download a backup?"
        description={
          <>
            <p>The file contains every WLAN passphrase and AP secret. The download is recorded in the audit log.</p>
            <p>Only one backup can download at a time.</p>
          </>
        }
        confirmLabel="Download"
        pending={busy}
        error={error}
        onConfirm={() => void download()}
      />
    </div>
  )
}
