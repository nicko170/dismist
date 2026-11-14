import { useQuery } from '@tanstack/react-query'
import { apKernelLogQuery } from '@/api/queries/aps'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { formatAbsolute, parseTime } from '@/lib/time'

// The AP's kernel-log ring (field 24.1, the last 200 lines; design §3.5,
// phase 4). Read-only, newest last, monospaced.

export function APKernelLog({ apId }: { apId: string }) {
  const query = useQuery({ ...apKernelLogQuery(apId), refetchInterval: 30_000, retry: false })
  if (query.isPending) return <SkeletonRows rows={3} />
  if (query.isError) {
    const status = (query.error as { status?: number }).status
    if (status === 404 || status === 501) return <p className="text-sm text-muted-foreground">This controller does not collect the AP log yet.</p>
    return <ErrorState error={query.error} title="Could not load the AP log" onRetry={() => void query.refetch()} />
  }
  if (!query.data.length) return <p className="text-sm text-muted-foreground">The AP has not reported any kernel log lines yet.</p>
  return (
    <div className="grid gap-2">
      <p className="text-xs text-muted-foreground">The last {query.data.length} lines the AP reported, oldest first.</p>
      <pre className="max-h-[70dvh] overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">
        {query.data.map((line, index) => {
          const at = parseTime(line.at)
          return (
            <span key={index} className="block">
              {at ? <span className="text-muted-foreground">{formatAbsolute(at)} </span> : null}
              {line.line}
            </span>
          )
        })}
      </pre>
    </div>
  )
}
