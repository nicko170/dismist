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
E2E_LIVE=1 npx playwright test                   # dismistd + tools/fakeap from this checkout (needs go, openssl)
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
  Live specs (real dismistd and fakeap) go in `e2e/live/` and read `liveState` from
  `e2e/live/global-setup.ts` (site ID, AP ID, admin token).

## Shared components (`src/components/app`, design §7.3)

`/dev/kit` (mock and dev builds) shows every one with synthetic data; `e2e/kit.spec.ts`
checks them at every width.

| Need | Use |
| --- | --- |
| A list | `ResponsiveList` (columns with `priority` 1/2/3, `card` roles, `rowLink`, `renderActions` → `RowMenu`), `ResponsiveListSkeleton` |
| Region states | `QueryState` (skeleton after 300 ms, `ErrorState` with Retry), `EmptyState`, `SkeletonTiles/Card/KV/Rows` |
| Status words | `StatusChip`, `SeverityChip`, `PushStateChip`, `PendingReasonChip`, `DisruptionChip`, `StateChip` (one table each in `StatusChip.tsx`) |
| Radio vocabulary | `ChannelBadge`, `SignalChip`, `UtilBar` + `UtilLegend`, `BandLabel` (`radio.tsx`) |
| Numbers | `StatTile`/`StatGrid`, `Sparkline`, `TimeSeries` (lazy uPlot with Show data), `KV` |
| Identifiers and times | `Identifier`/`Mono`/`CopyButton`, `RelativeTime` (shared 15 s clock) |
| Page structure | `PageHeader` (the H1), `Section` (collapsible card, hash anchor, summary line), `LinkTabs` (sub-navigation) |
| Attention | `AttentionList` (severity-sorted links) |
| Filters and search | `FilterSheet` (phone sheet with Apply and a count), `SearchInput` (debounced, `/` shortcut) |
| Saving | `StickyActionBar`, `ImpactNotice` (`impactLine`), `ImpactDialog` (dry-run preview with per-AP diffs), `PushProgress`, `notify` toasts (`toast.ts`, with Undo) |
| Confirmation | `useConfirm()` or `ConfirmDialog` (async `onConfirm`, `confirmText` type-to-confirm) |
| Diffs and JSON | `DiffView` (path/before/after, secrets hidden), `JsonDiff`, `JsonView`, `JsonEditor` (lint, completion from `paths`, `forbidden` mirror, Insert field) |
| Markers | `ExperimentalBadge`, `UnverifiedBadge`, `ShadowedBadge`, `InheritedValue`, `DirtyMarker` |
| Other | `DetailDrawer` (side panel / phone sheet), `QRCode` (lazy), `lib/csv.ts`, `lib/wifi.ts` (Wi-Fi QR string), `lib/ssh.ts` |

## Forms kit (`src/forms`, design §7.4)

```tsx
const form = useAppForm(schema, { defaultValues })              // zod + RHF, onTouched
const dryRun = useDryRun({ scope: ['wlan', id], values: useWatch({ control: form.control }),
  enabled: form.formState.isDirty && form.formState.isValid,
  run: (values, signal) => api.patch(url, toInput(values), { query: { dry_run: 1 }, signal }) })
const { submit, alert, staleDialog, pending } = useFormSubmit({ form, ifMatch: view.updated_rev,
  mutationFn: (values, { ifMatch }) => api.patch(url, toInput(values), { ifMatch }),
  successMessage: 'Saved', onReload: (current) => form.reset(toValues(current)) })
return (
  <AppForm form={form} onSubmit={submit} alert={alert} id="wlan-form">
    <UnsavedChangesGuard when={form.formState.isDirty} draft={{ key: `wlan:${id}`, read: form.getValues }} />
    <Section id="security" title="Security" collapsible summary="WPA2/WPA3 Personal">
      <SSIDField name="ssid" label="Network name" />
      <PasswordField name="psk" label="Passphrase" stored={view.has_psk} />
      <OptionField option={optionMeta['wlan.hidden']} experimentalAllowed={site.experimental} />
    </Section>
    <FormSaveBar form={form} formId="wlan-form" dryRun={dryRun} pending={pending} />
    {staleDialog}
  </AppForm>
)
```

Fields: `TextField` (`identifier` for MACs/IPs), `SSIDField` (UTF-8 bytes), `TextareaField`,
`PasswordField` (write-only, Generate, `autocomplete=new-password`), `SecretField` (AP secret,
paste or `secret.bin`), `NumberField` (keypad from `min`/`max`: fixed minus for negative-only
ranges, ± for signed; stores a number, `null` when empty), `SliderField`, `SwitchField`
(`invert` for "Disabled" keys), `SelectField`, `SegmentedField`, `ChipsField`, `ComboboxField`,
`CheckboxField`, `ListField`, `MACListField`, `SSHKeysField`, and `OptionField` (from
`gen/options.gen.ts`; `control` is one of text, ssid, password, secret, textarea, number,
slider, switch, select, segmented, chips, combobox, list, maclist, sshkeys). AP-scope fields
take `inherit={{ value: siteValue }}`: the form value is `null` while inheriting and an
Override switch copies the site value in. `validators` has zod helpers (IP, host, MAC, VLAN,
SSID bytes, passphrase rules, AP secret, SSH key, `numberInRange`). `useRestoredDraft`
offers back a draft saved when the session expired; `useExperimentalAck` gates Save on the
one-time experimental acknowledgement.

## Mocks and provisional types

`src/api/mocks` covers every endpoint of design §4.9 (phases 1 to 4) with `?dry_run=1`,
`If-Match`, field errors, `experimental_disabled`, `secret_required` (restore of revision
41 or earlier: answer with `secrets['networks.wlan-example.psk'] = { keep_current: true }`),
501 `unverified_command` for reboot and radio restart, and stream events after every commit.
Phase 2 to 4 shapes live in `src/api/provisional.ts` (re-exported by `@/api/types`) until
`internal/api` generates them; then the generated names replace them.
