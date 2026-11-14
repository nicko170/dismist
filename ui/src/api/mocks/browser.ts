import { setupWorker } from 'msw/browser'
import { configHandlers } from '@/features/settings/mocks/config-handlers'
import { dropStreams, restHandlers, streamHandlers } from './handlers'
import { mockState } from './state'

declare global {
  interface Window {
    __dismistMock?: {
      signIn(): void
      signOut(): void
      dropStreams(): void
    }
  }
}

/** Starts MSW in the browser (`--mode mock` only; never in production builds). */
export async function startMocks() {
  // Workstream F's typed config mocks come first so their phase-2 views win.
  const worker = setupWorker(...configHandlers, ...restHandlers, ...streamHandlers())
  await worker.start({
    serviceWorker: { url: '/mockServiceWorker.js' },
    onUnhandledFrame: 'bypass',
    quiet: true,
  })
  window.__dismistMock = {
    signIn: () => mockState.setSignedIn(true),
    signOut: () => mockState.setSignedIn(false),
    dropStreams,
  }
}
