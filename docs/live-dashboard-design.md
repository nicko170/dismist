# Dismist live dashboard stream

## Goal

Changes reported by an AP or saved by an operator reach the open dashboard
without waiting for its periodic refresh. Site selection remains the scope
boundary. Client, status, event, WLAN, and history views stay consistent with
the existing authenticated REST API and bbolt state.

## Stream contract

- `GET /api/sites/{site-id}/stream` is an authenticated same-origin SSE
  endpoint. The admin session cookie is used; tokens are never placed in URLs.
- It sends `event: update` with JSON containing the site ID, current state
  revision, observation time, and each AP's ID, connection state, applied
  revision, full/fast telemetry counters, and live associated-client count.
  The body contains no AP credential, WLAN passphrase, config JSON, client
  MAC, or history record. The UI refetches rich resources from REST.
- On connection it sends one update immediately. Thereafter it checks the
  selected site's bbolt revision and its APs' connection/telemetry counters
  at one-second intervals, sending at most one update per interval when the
  fingerprint changes. A minute boundary also refreshes history.
- It sends an SSE comment heartbeat every 15 seconds and closes a stream
  after 30 minutes so session expiry is rechecked on reconnect. Cancellation
  closes the stream promptly. It never subscribes a browser to another site.
- The UI creates one `EventSource` for the selected site while signed in,
  closes it on site switch/logout, coalesces update-triggered REST refreshes,
  and shows Live/Reconnecting connection state. The existing slower periodic
  refresh remains a fallback if a stream is unavailable.
- The browser refetches the site-scoped REST resources on an update rather
  than trusting a new parallel data model. History refreshes on stream
  updates with a bounded cadence. Real AP disconnects are shown separately
  from the UI stream's connection state.

## Verification

1. Unauthenticated streaming is refused by the web gate.
2. A site stream emits an initial update and a new update after a persisted
   site change or AP telemetry counter change; it emits no secrets.
3. Changing the selected site closes the previous stream. Reconnect and the
   fallback refresh recover from a dropped stream without duplicate timers.
4. A connected AP's live dashboard updates after a telemetry/status event while
   its WLAN and bbolt history remain unchanged by the test.
