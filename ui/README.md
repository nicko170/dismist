# dismist UI (`ui/`)

The v1 operator UI: React 19, TanStack Router (file routes) and Query, Tailwind 4
tokens with shadcn primitives, react-hook-form + zod. Design: `docs/design/v1.md`
§2, §3 and §7. The Go server embeds `ui/dist`; only a stub is committed, so CI and
the Dockerfile run `npm ci && npm run build` before `go build`.

```sh
npm ci
npm run dev          # Vite on :5173, proxies /api and /auth to DISMIST_API_ORIGIN (default http://127.0.0.1:8088)
npm run dev:mock     # same, with the MSW mocks (sign in with the token in src/api/mocks/data.ts)
npm run build        # tsc -b, then vite build into dist/ (assets under dist/_ui/)
npm run typecheck && npm run lint && npm test
npx playwright install chromium && npm run e2e   # mock build at 390, 768 (touch), 1024, 1024 (touch), 1440
npm run size         # initial JS budget (200 KB gzip), after a build
npm run routes       # regenerate src/routeTree.gen.ts (resolve conflicts this way, never by hand)
```

## Conventions for feature workstreams

- **Serving.** One build serves `/next/` (phases 0 and 1) and `/` (from M1); the router
  picks its basepath at runtime. Link with router paths (`/sites/home/aps`), never with
  `/next`. Public files live in `public/_ui/` so they sit beside the hashed assets.
- **Routes.** One file per screen under `src/routes/`; keep them thin (validateSearch,
  loader with `queryClient.ensureQueryData`, composition). Set `staticData.title` (page
  name for the title and breadcrumbs), `detail: true` on detail pages (phone back
  chevron) and `search: true` for the top-bar list search bound to `?q=`. Return
  `crumb` from a loader for data-driven names, or call `usePageTitle()`.
- **Headings.** Every page renders `PageHeader` (the H1 takes focus after navigation).
- **Data.** Query keys come from `src/api/keys.ts`; requests go through
  `src/api/client.ts` (`ApiError` with `status`, `code`, `fields`). Map 400/422 field
  errors onto forms with `applyFieldErrors`. Types: `src/api/types.ts` until
  `src/gen/api.gen.ts` lands, then from the generated file only.
- **Live data.** The site layout mounts the SSE stream; lists keyed `siteAPs`,
  `siteEvents(id, {})` (infinite, pages of `{events, next_before}`), `sitePush` and
  `pendingCount` are patched in place. Use `useStreamFallbackInterval()` as
  `refetchInterval` for data the stream keeps fresh.
- **Forms.** Controls use `text-base fine:md:text-sm` (the `Input` primitive does), 44 px
  targets below md, `useDraft()` while dirty so a session expiry keeps the draft.
- **Colours.** Tokens only; `npm run lint` fails on hex colours outside `src/styles/`.
- **Tests.** Add your routes to `e2e/routes.ts`; specs import `test`, `expect` and
  `checkPage` from `e2e/harness.ts` (no sideways scroll, 16 px inputs, tap targets, axe).
