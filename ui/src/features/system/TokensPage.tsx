import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { KeyRound, Plus } from 'lucide-react'
import { toast } from 'sonner'
import type { TokenCreated, TokenScope, TokenView } from '@/gen/api.gen'
import { tokensQuery, useCreateToken, useRevokeToken } from '@/api/queries/system'
import { applyFieldErrors, describeError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Chip } from '@/components/app/StatusChip'
import { CopyButton } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { ConfirmDialog, ResponsiveDialog } from '@/features/overview/kit/ConfirmDialog'
import { NativeSelect } from '@/features/overview/kit/FilterBar'
import { ResponsiveList, type Column } from '@/features/overview/kit/ResponsiveList'

// API tokens (design §3.14, §4.8): name, scope, created, last used; the
// secret is shown once at creation with a copy button; revoke per token.

function CreateTokenDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const create = useCreateToken()
  const [name, setName] = useState('')
  const [scope, setScope] = useState<TokenScope>('read')
  const [nameError, setNameError] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [created, setCreated] = useState<TokenCreated | null>(null)
  const close = (next: boolean) => {
    if (next) return onOpenChange(true)
    onOpenChange(false)
    setName('')
    setScope('read')
    setNameError(null)
    setFormError(null)
    setCreated(null)
    create.reset()
  }
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (!name.trim()) {
      setNameError('Name the token, for example after the laptop or script that uses it.')
      return
    }
    create.mutate(
      { name: name.trim(), scope },
      {
        onSuccess: (token) => setCreated(token),
        onError: (error) => {
          let mapped: string | null = null
          const rest = applyFieldErrors(error, ((_: string, e: { message: string }) => (mapped = e.message)) as never, ['name'])
          setNameError(mapped)
          setFormError(rest.length ? rest.join(' ') : null)
        },
      },
    )
  }
  if (created) {
    return (
      <ResponsiveDialog open={open} onOpenChange={close} title="Token created" footer={<Button onClick={() => close(false)}>Done</Button>}>
        <p className="text-sm">Copy the token now. dismist stores only a hash of it and cannot show it again.</p>
        <div className="flex min-w-0 items-center gap-1 rounded-md border bg-muted/50 py-1 pr-1 pl-3">
          <code className="min-w-0 flex-1 font-mono text-sm break-all">{created.token}</code>
          <CopyButton value={created.token} label="Copy token" />
        </div>
        <p className="text-xs text-muted-foreground">
          Use it as <code className="font-mono">Authorization: Bearer …</code>, or save it to a file for <code className="font-mono">dismist ap33 onboard --token-file</code>.
        </p>
      </ResponsiveDialog>
    )
  }
  return (
    <ResponsiveDialog open={open} onOpenChange={close} title="Create an API token" dismissable={!create.isPending}>
      <form noValidate onSubmit={submit} className="grid gap-4">
        <div className="grid gap-1.5">
          <label htmlFor="token-name" className="text-sm font-medium">
            Name
          </label>
          <Input
            id="token-name"
            value={name}
            onChange={(e) => {
              setName(e.target.value)
              setNameError(null)
            }}
            maxLength={64}
            autoComplete="off"
            aria-invalid={nameError ? true : undefined}
            aria-describedby={nameError ? 'token-name-error' : undefined}
          />
          {nameError ? (
            <p id="token-name-error" role="alert" className="text-sm text-destructive">
              {nameError}
            </p>
          ) : null}
        </div>
        <NativeSelect
          label="Scope"
          value={scope}
          allLabel={null}
          options={[
            { value: 'read', label: 'Read: GET requests only' },
            { value: 'admin', label: 'Admin: everything a signed-in session can do' },
          ]}
          onChange={(v) => setScope(v as TokenScope)}
        />
        {formError ? (
          <p role="alert" className="text-sm text-destructive">
            {formError}
          </p>
        ) : null}
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button variant="outline" onClick={() => close(false)} disabled={create.isPending}>
            Cancel
          </Button>
          <Button type="submit" variant="brand" disabled={create.isPending}>
            {create.isPending ? 'Creating…' : 'Create token'}
          </Button>
        </div>
      </form>
    </ResponsiveDialog>
  )
}

export function TokensPage() {
  const tokens = useQuery(tokensQuery)
  const revoke = useRevokeToken()
  const [creating, setCreating] = useState(false)
  const [revoking, setRevoking] = useState<TokenView | null>(null)
  const columns: Column<TokenView>[] = [
    { id: 'name', header: 'Name', render: (t) => <span className="font-medium break-words">{t.name}</span> },
    { id: 'scope', header: 'Scope', render: (t) => <Chip tone={t.scope === 'admin' ? 'warn' : 'idle'}>{t.scope}</Chip> },
    { id: 'created', header: 'Created', render: (t) => <RelativeTime value={t.created} /> },
    { id: 'used', header: 'Last used', render: (t) => <RelativeTime value={t.last_used} /> },
    {
      id: 'revoke',
      header: <span className="sr-only">Actions</span>,
      align: 'end',
      render: (t) => (
        <Button variant="outline" size="sm" onClick={() => setRevoking(t)} aria-label={`Revoke ${t.name}`}>
          Revoke
        </Button>
      ),
    },
  ]
  return (
    <div className="grid min-w-0 gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">Tokens let the CLI and scripts use the API without signing in.</p>
        <Button variant="brand" onClick={() => setCreating(true)}>
          <Plus />
          Create token
        </Button>
      </div>
      {tokens.isPending ? (
        <SkeletonRows rows={2} />
      ) : tokens.isError ? (
        <ErrorState error={tokens.error} title="Could not load API tokens" onRetry={() => void tokens.refetch()} />
      ) : tokens.data.length === 0 ? (
        <EmptyState icon={<KeyRound />} title="No API tokens yet" action={<Button onClick={() => setCreating(true)}>Create token</Button>}>
          Create one for <code className="font-mono">dismist ap33 onboard</code>.
        </EmptyState>
      ) : (
        <ResponsiveList
          caption="API tokens"
          rows={tokens.data}
          rowKey={(t) => t.id}
          columns={columns}
          renderCard={(t) => (
            <div className="flex min-w-0 items-start justify-between gap-3">
              <div className="grid min-w-0 gap-1">
                <span className="font-medium break-words">{t.name}</span>
                <span className="text-xs text-muted-foreground">
                  <Chip tone={t.scope === 'admin' ? 'warn' : 'idle'}>{t.scope}</Chip> · created <RelativeTime value={t.created} /> · used <RelativeTime value={t.last_used} />
                </span>
              </div>
              <Button variant="outline" size="sm" onClick={() => setRevoking(t)} aria-label={`Revoke ${t.name}`}>
                Revoke
              </Button>
            </div>
          )}
        />
      )}
      <CreateTokenDialog open={creating} onOpenChange={setCreating} />
      <ConfirmDialog
        open={Boolean(revoking)}
        onOpenChange={(o) => !o && setRevoking(null)}
        title={`Revoke ${revoking?.name ?? 'token'}?`}
        description="Anything using this token (the CLI, scripts) stops working at once."
        confirmLabel="Revoke token"
        destructive
        pending={revoke.isPending}
        error={revoke.error ? describeError(revoke.error).message : null}
        onConfirm={() =>
          revoking &&
          revoke.mutate(revoking.id, {
            onSuccess: () => {
              toast.success(`${revoking.name} revoked`)
              setRevoking(null)
            },
          })
        }
      />
    </div>
  )
}
