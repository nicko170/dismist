import { useQuery } from '@tanstack/react-query'
import { PageHeader } from '@/components/app/PageHeader'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { sitesQuery } from '@/features/shell/queries'
import { usePageTitle } from '@/features/shell/title'
import { normaliseMac } from '@/lib/mac'
import { RegisterForm } from './RegisterForm'
import { VerifyPanel } from './VerifyPanel'

export type RegisterSearch = { id?: string; site?: string; name?: string; step?: 'form' | 'verify' }

/** /onboard/register: the form, or with ?step=verify&id= the live Verify panel (design §3.15). */
export function RegisterPage({ search }: { search: RegisterSearch }) {
  const verifyId = search.step === 'verify' && search.id ? (normaliseMac(search.id) ?? search.id) : null
  usePageTitle(verifyId ? 'Verify AP' : null)
  return (
    <div className="mx-auto grid w-full max-w-2xl gap-5">
      <PageHeader
        title={verifyId ? 'Verify the AP' : 'Register an AP'}
        meta={verifyId ? 'Live: updates as the AP connects, fetches and applies its config.' : 'For an AP you prepared yourself. The CLI registers APs for you.'}
      />
      <section className="rounded-xl border bg-card p-4 sm:p-6" aria-label={verifyId ? 'Verification' : 'Registration'}>
        {verifyId ? <VerifyPanel apId={verifyId} siteId={search.site} /> : <RegisterFormLoader search={search} />}
      </section>
    </div>
  )
}

function RegisterFormLoader({ search }: { search: RegisterSearch }) {
  const sites = useQuery(sitesQuery)
  if (sites.isPending) return <SkeletonRows rows={5} />
  if (sites.isError) return <ErrorState error={sites.error} title="Could not load sites" onRetry={() => void sites.refetch()} />
  return <RegisterForm sites={sites.data} defaults={{ id: search.id, site: search.site, name: search.name }} />
}
