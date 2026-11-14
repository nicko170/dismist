import { Link } from '@tanstack/react-router'
import { buttonVariants } from '@/components/ui/button'
import { PageHeader } from './PageHeader'

export function NotFound({
  title = 'Page not found',
  detail = 'Nothing lives at this address. It may have moved, or the link may be wrong.',
  to = '/',
  action = 'Go to the start',
}: {
  title?: string
  detail?: string
  to?: string
  action?: string
}) {
  return (
    <div className="grid max-w-xl gap-4 py-6">
      <PageHeader title={title} />
      <p className="text-sm text-muted-foreground">{detail}</p>
      <Link to={to as never} className={buttonVariants({ variant: 'outline', className: 'justify-self-start' })}>
        {action}
      </Link>
    </div>
  )
}

export function SiteNotFound() {
  return (
    <NotFound title="Site not found" detail="No site has this ID. It may have been deleted." to="/sites" action="All sites" />
  )
}
