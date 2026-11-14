import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import type { AuthSessionView } from '@/gen/api.gen'
import { keys } from '@/api/keys'
import { sessionsQuery, useRevokeAllSessions, useRevokeSession } from '@/api/queries/system'
import { describeError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/app/StatusChip'
import { Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { ConfirmDialog } from '@/features/overview/kit/ConfirmDialog'
import { ResponsiveList, type Column } from '@/features/overview/kit/ResponsiveList'

// Signed-in sessions (design §3.14): created, expires, last used, remote
// address, user agent, "this session", revoke per row, sign out everywhere.

export function browserName(ua: string): string {
  if (!ua) return 'Unknown browser'
  const os = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : /curl\//.test(ua) ? 'curl' : 'Browser'
  return os ? `${browser} on ${os}` : browser
}

export function SessionsPage() {
  const sessions = useQuery(sessionsQuery)
  const revoke = useRevokeSession()
  const revokeAll = useRevokeAllSessions()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [target, setTarget] = useState<AuthSessionView | null>(null)
  const [all, setAll] = useState(false)

  const signedOut = () => {
    qc.clear()
    qc.setQueryData(keys.session, { authenticated: false, expires_at: null })
    void navigate({ to: '/login', replace: true })
  }

  const columns: Column<AuthSessionView>[] = [
    {
      id: 'agent',
      header: 'Browser',
      render: (s) => (
        <span className="grid gap-0.5">
          <span className="font-medium" title={s.user_agent}>
            {browserName(s.user_agent)}
          </span>
          {s.current ? <Chip tone="good">This session</Chip> : null}
        </span>
      ),
    },
    { id: 'remote', header: 'From', render: (s) => (s.remote ? <Mono className="text-xs">{s.remote}</Mono> : '–') },
    { id: 'used', header: 'Last used', render: (s) => <RelativeTime value={s.last_used} /> },
    { id: 'created', header: 'Signed in', priority: 2, render: (s) => <RelativeTime value={s.created} /> },
    { id: 'expires', header: 'Expires', priority: 2, render: (s) => <RelativeTime value={s.expires} /> },
    {
      id: 'revoke',
      header: <span className="sr-only">Actions</span>,
      align: 'end',
      render: (s) => (
        <Button variant="outline" size="sm" onClick={() => setTarget(s)} aria-label={`Revoke ${browserName(s.user_agent)} session`}>
          {s.current ? 'Sign out' : 'Revoke'}
        </Button>
      ),
    },
  ]

  return (
    <div className="grid min-w-0 gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">Web sessions signed in with the admin token. Sessions survive controller restarts.</p>
        <Button variant="outline" onClick={() => setAll(true)} disabled={!sessions.data?.length}>
          Sign out everywhere
        </Button>
      </div>
      {sessions.isPending ? (
        <SkeletonRows rows={2} />
      ) : sessions.isError ? (
        <ErrorState error={sessions.error} title="Could not load sessions" onRetry={() => void sessions.refetch()} />
      ) : sessions.data.length === 0 ? (
        <EmptyState title="No signed-in sessions">You may be using an API token.</EmptyState>
      ) : (
        <ResponsiveList
          caption="Signed-in sessions"
          rows={sessions.data}
          rowKey={(s) => s.id}
          columns={columns}
          renderCard={(s) => (
            <div className="flex min-w-0 items-start justify-between gap-3">
              <div className="grid min-w-0 gap-1">
                <span className="flex flex-wrap items-center gap-2 font-medium">
                  {browserName(s.user_agent)} {s.current ? <Chip tone="good">This session</Chip> : null}
                </span>
                <span className="text-xs text-muted-foreground">
                  {s.remote ? <Mono>{s.remote}</Mono> : null} · used <RelativeTime value={s.last_used} /> · expires <RelativeTime value={s.expires} />
                </span>
              </div>
              <Button variant="outline" size="sm" onClick={() => setTarget(s)} aria-label={`Revoke ${browserName(s.user_agent)} session`}>
                {s.current ? 'Sign out' : 'Revoke'}
              </Button>
            </div>
          )}
        />
      )}
      <ConfirmDialog
        open={Boolean(target)}
        onOpenChange={(o) => !o && setTarget(null)}
        title={target?.current ? 'Sign out this session?' : 'Revoke this session?'}
        description={target?.current ? 'You will need the admin token to sign in again.' : `${browserName(target?.user_agent ?? '')} is signed out at once.`}
        confirmLabel={target?.current ? 'Sign out' : 'Revoke session'}
        destructive
        pending={revoke.isPending}
        error={revoke.error ? describeError(revoke.error).message : null}
        onConfirm={() =>
          target &&
          revoke.mutate(target.id, {
            onSuccess: () => {
              if (target.current) return signedOut()
              toast.success('Session revoked')
              setTarget(null)
            },
          })
        }
      />
      <ConfirmDialog
        open={all}
        onOpenChange={setAll}
        title="Sign out everywhere?"
        description="Every web session ends, including this one. API tokens keep working; revoke them on the Tokens page."
        confirmLabel="Sign out everywhere"
        destructive
        pending={revokeAll.isPending}
        error={revokeAll.error ? describeError(revokeAll.error).message : null}
        onConfirm={() => revokeAll.mutate(undefined, { onSuccess: signedOut })}
      />
    </div>
  )
}
