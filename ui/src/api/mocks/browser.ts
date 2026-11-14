import { setupWorker } from 'msw/browser'
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
  const worker = setupWorker(...restHandlers, ...streamHandlers())
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
