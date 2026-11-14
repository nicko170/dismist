import { describe, expect, it } from 'vitest'
import raw from '../public/_ui/manifest.webmanifest?raw'
import { resolveBasepath } from './router'

// Phases 0 and 1 serve this app at /next/ and the legacy app at /, so an
// installed app must start at /next/. At M1, / serves this app and /next/*
// redirects to it; start_url can then become / (design §2.4).
describe('PWA manifest', () => {
  const manifest = JSON.parse(raw) as { start_url: string; scope: string }

  it('starts the installed app on this app, not the legacy one', () => {
    expect(manifest.start_url).toBe('/next/')
    expect(resolveBasepath(new URL(manifest.start_url, 'https://dismist.example').pathname)).toBe('/next')
  })

  it('keeps its start URL inside its scope', () => {
    expect(manifest.start_url.startsWith(manifest.scope)).toBe(true)
  })
})
