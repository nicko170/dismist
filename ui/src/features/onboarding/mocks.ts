import { http, HttpResponse, type HttpHandler } from 'msw'

// MSW handlers for the onboarding endpoints that the shared mock set
// (api/mocks/handlers.ts, workstream D) does not cover yet. D spreads
// `onboardingHandlers` into restHandlers; until then the hub's Dismiss
// button and the CA bundle link answer 404 in the mock build.
// Synthetic data only.

const dismissed = new Set<string>()

export const SYNTHETIC_CA_BUNDLE = `-----BEGIN CERTIFICATE-----
MIIBsynthetic0000000000000000000000000000000000000000000000000000
-----END CERTIFICATE-----
`

export const onboardingHandlers: HttpHandler[] = [
  http.delete('/api/aps/pending/:id', ({ params }) => {
    dismissed.add(String(params.id))
    return new HttpResponse(null, { status: 204 })
  }),
  http.get('/api/onboarding/ca-bundle.pem', () =>
    new HttpResponse(SYNTHETIC_CA_BUNDLE, {
      headers: { 'Content-Type': 'application/x-pem-file', 'Content-Disposition': 'attachment; filename="cacerts-production.pem"' },
    }),
  ),
]

/** IDs dismissed through the mock (tests reset it). */
export function mockDismissed(): Set<string> {
  return dismissed
}
