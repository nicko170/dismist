import { createFileRoute } from '@tanstack/react-router'
import { TokensPage } from '@/features/system/TokensPage'

export const Route = createFileRoute('/system/tokens')({
  staticData: { title: 'API tokens' },
  component: TokensPage,
})
