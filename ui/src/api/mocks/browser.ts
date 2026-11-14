import { setupWorker } from 'msw/browser'
import { onboardingHandlers } from '@/features/onboarding/mocks'
import { screenHandlers } from '@/features/overview/mocks/handlers'
import { configHandlers as featureConfigHandlers } from '@/features/settings/mocks/config-handlers'
import type { APView, EventView } from '../types'
import * as data from './data'
import { dropStreams, emit, failStreams, restHandlers, streamHandlers, toLive, toSummary } from './handlers'
import { configHandlers } from './handlers-config'
import { mockState } from './state'

/** Controls the Playwright specs drive (window.__dismistMock, mock builds only). */
export type MockControls = {
  signIn(): void
  signOut(): void
  /** Errors every open stream once (the browser then retries). */
  dropStreams(): void
  /** While on, the stream endpoint fails at once: the controller is down. */
  failStreams(on: boolean): void
  /** Changes an AP and sends its `ap` event to the site's streams. */
  updateAP(id: string, patch: Partial<APView>): void
  /** Stores an event and sends its summary. */
  addEvent(event: Partial<EventView> & Pick<EventView, 'id' | 'ap_id' | 'title'>): void
  /** Sets the pending-AP count and sends `pending`. */
  setPending(count: number): void
}

declare global {
  interface Window {
    __dismistMock?: MockControls
  }
}

/** Starts MSW in the browser (`--mode mock` only; never in production builds). */
export async function startMocks() {
  // Workstream F's typed config mocks come first so their phase-2 views win
  // (they fall through for sites and APs they do not hold); the onboarding
  // and monitoring-screen handlers fill what the shared set lacks.
  const worker = setupWorker(
    ...featureConfigHandlers,
    ...onboardingHandlers,
    ...restHandlers,
    ...configHandlers,
    ...screenHandlers,
    ...streamHandlers(),
  )
  await worker.start({
    serviceWorker: { url: '/mockServiceWorker.js' },
    onUnhandledFrame: 'bypass',
    quiet: true,
  })
  window.__dismistMock = {
    signIn: () => mockState.setSignedIn(true),
    signOut: () => mockState.setSignedIn(false),
    dropStreams,
    failStreams,
    updateAP(id, patch) {
      const ap = data.aps.find((candidate) => candidate.id === id)
      if (!ap) return
      Object.assign(ap, patch)
      emit(ap.site_id, { type: 'ap', data: toLive(ap) })
    },
    addEvent(partial) {
      const ap = data.aps.find((candidate) => candidate.id === partial.ap_id)
      const now = new Date().toISOString()
      const event: EventView = {
        site_id: ap?.site_id ?? 'home',
        ap_name: ap?.name ?? '',
        kind: 'connection',
        pace_type: null,
        severity: 'info',
        detail: '',
        client_mac: '',
        wlan_id: '',
        first_seen: now,
        last_seen: now,
        count: 1,
        resolved_by: null,
        data: {},
        ...partial,
      }
      const index = data.events.findIndex((candidate) => candidate.id === event.id)
      if (index >= 0) data.events.splice(index, 1)
      data.events.unshift(event)
      emit(event.site_id, { type: 'event', data: toSummary(event) })
    },
    setPending(count) {
      for (const site of data.sites) emit(site.id, { type: 'pending', data: { count } })
    },
  }
}
