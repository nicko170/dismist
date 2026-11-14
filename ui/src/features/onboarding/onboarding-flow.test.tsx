import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Outlet, RouterProvider, createMemoryHistory, createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import type { APDetail, APInput, OnboardingView, PendingAP, SiteSummary } from '@/gen/api.gen'
import { OnboardHub } from './OnboardHub'
import { RegisterPage, type RegisterSearch } from './RegisterPage'
import { mockDismissed, onboardingHandlers } from './mocks'

// The hub, the Register form and the Verify panel against MSW (synthetic
// data), inside a small router with the same paths as routes/onboard/.

const hex = 'a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebf'
const now = new Date().toISOString()

const sites: SiteSummary[] = [
  { id: 'home', name: 'Home', country: 'AU', timezone: 'Australia/Sydney', counts: { aps: 1, aps_online: 1, clients: 3, wlans: 1, pending_aps: 1 } },
]
const pendingRow: PendingAP = {
  id: '02-aa-00-00-00-20',
  model: 'AP33-WW',
  sku: 'AP33-WW',
  serial: 'A0000020',
  firmware: 'apfw-0.15.35064-noro-84df',
  local_ip: '192.0.2.30',
  remote_address: '198.51.100.7',
  first_seen: now,
  last_seen: now,
  attempts: 3,
  reason: 'unregistered',
    dismissed: false,
    dismissed_until: null,
}
const onboarding: OnboardingView = {
  wss_url: 'wss://ap.example.net/ws',
  dns_mode: false,
  ca_pems: [{ name: 'ISRG Root X1', pem: '-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----\n' }],
  cloud_ini: '[[cloud]]\n  url = "wss://ap.example.net/ws"\n',
}

function detail(input: APInput, patch: Partial<APDetail> = {}): APDetail {
  return {
    id: input.id,
    name: input.name,
    site_id: input.site_id,
    model: '',
    model_source: 'record',
    firmware: '',
    firmware_tested: false,
    status: 'never_connected',
    flags: [],
    connected: false,
    connected_at: null,
    last_seen: null,
    lan_ip: '',
    remote_address: '',
    behind_nat: false,
    pinned_ip: input.management_ip ?? '',
    has_secret: true,
    config_revision: 43,
    applied_revision: 0,
    last_error: '',
    client_count: 0,
    uptime_seconds: 0,
    radios: [],
    updated_rev: 1,
    wlans_live: [],
    ...patch,
  } as APDetail
}

let posted: APInput[] = []
let record: APDetail | null = null
let pending: PendingAP[] = []

const server = setupServer(
  ...onboardingHandlers,
  http.get('/api/sites', () => HttpResponse.json(sites)),
  http.get('/api/onboarding', () => HttpResponse.json(onboarding)),
  http.get('/api/system/tokens', () => HttpResponse.json({ tokens: [] })),
  http.get('/api/aps/pending', ({ request }) =>
    HttpResponse.json(new URL(request.url).searchParams.get('dismissed') ? [] : pending.filter((p) => !mockDismissed().has(p.id))),
  ),
  http.get('/api/aps', () => HttpResponse.json(record ? [record] : [])),
  http.get('/api/aps/:id', ({ params }) =>
    record && record.id === params.id ? HttpResponse.json(record) : HttpResponse.json({ error: 'AP not found.', code: 'not_found' }, { status: 404 }),
  ),
  http.post('/api/aps', async ({ request }) => {
    const input = (await request.json()) as APInput
    if (input.id === '02-aa-00-00-00-99') {
      return HttpResponse.json({ error: 'Check the highlighted fields.', code: 'invalid', fields: { secret: 'That secret does not decode to 32 bytes.' } }, { status: 400 })
    }
    posted.push(input)
    record = detail(input)
    return HttpResponse.json({ revision: 43, object: record, impact: { aps: [], disruption: 'none', summary: 'No AP changes' }, warnings: [] }, { status: 201 })
  }),
)

beforeAll(() => server.listen({ onUnhandledFrame: 'error' }))
afterAll(() => server.close())
beforeEach(() => {
  posted = []
  record = null
  pending = [pendingRow]
  mockDismissed().clear()
  // jsdom has no EventSource: the site stream reports "offline" and the panel polls.
})
afterEach(() => server.resetHandlers())

function renderAt(path: string) {
  const root = createRootRoute({ component: () => <Outlet /> })
  const onboard = createRoute({
    getParentRoute: () => root,
    path: '/onboard',
    validateSearch: (search: Record<string, unknown>): { site?: string } => (typeof search.site === 'string' ? { site: search.site } : {}),
  })
  const hub = createRoute({
    getParentRoute: () => onboard,
    path: '/',
    component: function Hub() {
      const { site } = hub.useSearch()
      return <OnboardHub siteId={site} />
    },
  })
  const register = createRoute({
    getParentRoute: () => onboard,
    path: '/register',
    validateSearch: (search: Record<string, unknown>) => search as RegisterSearch,
    component: function Register() {
      return <RegisterPage search={register.useSearch()} />
    },
  })
  const router = createRouter({
    routeTree: root.addChildren([onboard.addChildren([hub, register])]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router as never} />
    </QueryClientProvider>,
  )
  return router
}

describe('onboarding hub', () => {
  it('lists waiting APs, builds the CLI command and shows the trust material', async () => {
    const user = userEvent.setup()
    renderAt('/onboard?site=home')
    expect(await screen.findByRole('heading', { level: 1, name: 'Add an AP' })).toBeInTheDocument()
    const list = await screen.findByRole('list', { name: 'Waiting APs' })
    expect(within(list).getByText('192.0.2.30')).toBeInTheDocument()
    expect(within(list).getByText('Not registered')).toBeInTheDocument()

    await user.type(screen.getByLabelText('AP name'), 'Front room')
    await user.type(screen.getByLabelText('AP LAN address'), '192.0.2.10')
    const command = screen.getByLabelText('Onboard command')
    expect(command.textContent).toBe(
      `dismist ap33 onboard root@192.0.2.10 --controller ${window.location.origin} --token-file ~/.config/dismist/token --site Home --name 'Front room'`,
    )
    expect(await screen.findByRole('link', { name: 'Create an API token' })).toBeInTheDocument()
    expect(await screen.findByLabelText('cloud.ini')).toHaveTextContent('wss://ap.example.net/ws')
    expect(screen.getByRole('link', { name: /Download CA bundle/ })).toHaveAttribute('href', '/api/onboarding/ca-bundle.pem')
  })

  it('dismisses a waiting AP', async () => {
    const user = userEvent.setup()
    renderAt('/onboard')
    const list = await screen.findByRole('list', { name: 'Waiting APs' })
    await user.click(within(list).getByRole('button', { name: 'Dismiss 02-aa-00-00-00-20' }))
    await waitFor(() => expect(screen.queryByRole('list', { name: 'Waiting APs' })).not.toBeInTheDocument())
    expect(mockDismissed().has('02-aa-00-00-00-20')).toBe(true)
    expect(screen.getByText('No AP is waiting.')).toBeInTheDocument()
  })
})

describe('register and verify', () => {
  it('adopts a pending AP, then shows the live Verify panel', async () => {
    const user = userEvent.setup()
    const router = renderAt('/onboard?site=home')
    const list = await screen.findByRole('list', { name: 'Waiting APs' })
    await user.click(within(list).getByRole('link', { name: 'Adopt 02-aa-00-00-00-20' }))

    expect(await screen.findByRole('heading', { level: 1, name: 'Register an AP' })).toBeInTheDocument()
    expect(screen.getByLabelText('AP ID')).toHaveValue('02-aa-00-00-00-20')
    expect(screen.getByLabelText('Name')).toHaveValue('AP 00-20')
    await waitFor(() => expect(screen.getByLabelText('Site')).toHaveValue('home'))
    await user.clear(screen.getByLabelText('Name'))
    await user.type(screen.getByLabelText('Name'), 'Garage')
    await user.type(screen.getByLabelText('Secret'), hex)
    await user.click(screen.getByRole('button', { name: 'Register' }))

    expect(await screen.findByRole('heading', { level: 1, name: 'Verify the AP' })).toBeInTheDocument()
    expect(posted).toEqual([{ id: '02-aa-00-00-00-20', name: 'Garage', site_id: 'home', secret: hex }])
    expect(router.state.location.search).toMatchObject({ step: 'verify', id: '02-aa-00-00-00-20', site: 'home' })
    const steps = screen.getByRole('list', { name: 'Verification steps' })
    expect(within(steps).getByText('Waiting for hello')).toBeInTheDocument()

    // The AP connects, fetches and applies: the panel follows (polling here).
    record = detail(posted[0], {
      status: 'in_sync',
      connected: true,
      connected_at: now,
      last_seen: now,
      model: 'AP33-WW',
      firmware: 'apfw-0.15.35064-noro-84df',
      applied_revision: 43,
      lan_ip: '192.0.2.30',
      wlans_live: [
        { wlan_id: 'w1', ssid: 'Example', band: '2.4', bssid: '02-aa-00-00-20-00', dev: 'wl0.1', clients: 0 },
        { wlan_id: 'w1', ssid: 'Example', band: '5', bssid: '02-aa-00-00-20-10', dev: 'wl1.1', clients: 0 },
      ],
    })
    expect(await within(steps).findByText('Broadcasting Example on 2.4 and 5 GHz', {}, { timeout: 8000 })).toBeInTheDocument()
    expect(within(steps).getByText('Authenticated (AP33-WW, apfw-0.15.35064-noro-84df)')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open Garage' })).toHaveAttribute('href', '/sites/home/aps/02-aa-00-00-00-20')
  }, 15_000)

  it('validates locally and maps server field errors', async () => {
    const user = userEvent.setup()
    renderAt('/onboard/register?site=home')
    await user.click(await screen.findByRole('button', { name: 'Register' }))
    expect(await screen.findByText(/Enter the AP’s ID/)).toBeInTheDocument()
    expect(screen.getByText(/Paste the secret/)).toBeInTheDocument()

    await user.type(screen.getByLabelText('AP ID'), '02AA00000099')
    expect(screen.getByText('02-aa-00-00-00-99')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Name'), 'Shed')
    await user.type(screen.getByLabelText('Secret'), hex)
    await user.click(screen.getByRole('button', { name: 'Register' }))
    expect(await screen.findByText('That secret does not decode to 32 bytes.')).toBeInTheDocument()
    expect(screen.getByLabelText('Secret')).toHaveAttribute('aria-invalid', 'true')
  })

  it('reads secret.bin and refuses a file of the wrong size', async () => {
    const user = userEvent.setup({ applyAccept: false })
    renderAt('/onboard/register?site=home')
    await screen.findByRole('button', { name: 'Register' })
    const input = document.body.querySelector<HTMLInputElement>('input[type="file"]')!
    await user.upload(input, new File([new Uint8Array(16)], 'eeprom.bin'))
    expect(await screen.findByText(/eeprom.bin is 16 bytes/)).toBeInTheDocument()
    await user.upload(input, new File([Uint8Array.from({ length: 32 }, (_, i) => 0xa0 + i)], 'secret.bin'))
    expect(await screen.findByText('secret.bin loaded (32 bytes).')).toBeInTheDocument()
    expect(screen.getByLabelText('Secret')).toHaveValue(hex)
  })

  it('says so when the AP in the Verify link is not registered', async () => {
    renderAt('/onboard/register?step=verify&id=02-aa-00-00-00-77')
    expect(await screen.findByText('No AP with ID 02-aa-00-00-00-77 is registered.')).toBeInTheDocument()
  })
})
