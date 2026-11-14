import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConfirmDialog } from './ConfirmDialog'
import { toCsv } from './csv'
import { compact, pickEnum, pickString, pickTime } from './search'
import { signalFromRssi } from './SignalChip'
import { niceTicks, summarise, TimeSeries } from './TimeSeries'
import { normaliseUtil, UtilBar } from './UtilBar'
import { daysLeft, expiryText, expiryTone } from '@/features/system/cert'
import { validateSite } from '../NewSiteDialog'

describe('search params', () => {
  it('drops unknown and invalid values', () => {
    expect(pickEnum('offline', ['online', 'offline'] as const)).toBe('offline')
    expect(pickEnum('bogus', ['online'] as const)).toBeUndefined()
    expect(pickString('  x  ')).toBe('x')
    expect(pickString('')).toBeUndefined()
    expect(pickTime('nope')).toBeUndefined()
    expect(compact({ a: undefined, b: 'x', c: '' })).toEqual({ b: 'x' })
  })
})

describe('csv', () => {
  it('quotes and neutralises formulas', () => {
    expect(toCsv(['a', 'b'], [['x,y', '=1+1']])).toBe('a,b\r\n"x,y",\'=1+1\r\n')
  })
})

describe('signal and cert thresholds', () => {
  it('buckets RSSI', () => {
    expect([-55, -60, -61, -67, -70, -76].map(signalFromRssi)).toEqual(['excellent', 'excellent', 'good', 'good', 'fair', 'poor'])
    expect(signalFromRssi(0)).toBeNull()
  })
  it('tones certificate expiry (amber < 21 d, red < 14 d)', () => {
    const now = Date.parse('2026-11-14T00:00:00Z')
    expect(daysLeft('2026-12-14T00:00:00Z', now)).toBe(30)
    expect([30, 20, 13, null].map((d) => expiryTone(d))).toEqual(['good', 'warn', 'crit', 'idle'])
    expect(expiryText(-2)).toBe('Expired 2 d ago')
  })
})

describe('charts', () => {
  it('picks round y ticks', () => {
    expect(niceTicks(23)).toEqual([0, 10, 20, 30])
    expect(niceTicks(0)).toEqual([0, 1])
  })
  it('summarises a series in words', () => {
    const t = Date.parse('2026-11-14T10:00:00Z')
    expect(summarise([{ t, v: 3 }, { t: t + 60_000, v: 9 }, { t: t + 120_000, v: 5 }], 'clients')).toMatch(/^Peak 9 clients at .+; now 5 clients\.$/)
    expect(summarise([{ t, v: null }], 'clients')).toBe('No clients data in this range.')
  })
  it('renders the chart with a hidden summary and a data table', () => {
    const t = Date.parse('2026-11-14T10:00:00Z')
    render(<TimeSeries points={[{ t, v: 1 }, { t: t + 600_000, v: 4 }]} unit="clients" label="Clients" />)
    expect(screen.getByRole('img', { name: 'Clients' })).toBeInTheDocument()
    expect(screen.getByText(/Peak 4 clients/)).toBeInTheDocument()
    expect(screen.getByText('Show data')).toBeInTheDocument()
  })
  it('draws a lone sample, and one between gaps, as a dot', () => {
    const t = Date.parse('2026-11-14T10:00:00Z')
    const { container, unmount } = render(<TimeSeries points={[{ t, v: 2 }]} unit="clients" label="Clients" />)
    expect(container.querySelectorAll('circle[data-point]')).toHaveLength(1)
    expect(container.querySelectorAll('path')).toHaveLength(0)
    unmount()
    const gappy = render(
      <TimeSeries
        points={[{ t, v: 1 }, { t: t + 60_000, v: 2 }, { t: t + 120_000, v: null }, { t: t + 180_000, v: 3 }, { t: t + 240_000, v: null }]}
        unit="clients"
        label="Clients"
      />,
    )
    expect(gappy.container.querySelectorAll('path')).toHaveLength(1)
    expect(gappy.container.querySelectorAll('circle[data-point]')).toHaveLength(1)
  })
  it('splits utilisation and lists every part in words', () => {
    // The API's fractions (0 to 1) and part keys.
    expect(normaliseUtil(0.4, { tx: 0.1, rx_in_bss: 0.2, unknown_wifi: 0.03, undecodable_wifi: 0.02 }).idle).toBeCloseTo(65)
    expect(normaliseUtil(0.2, {}).busy).toBeCloseTo(20)
    render(<UtilBar util={0.12} parts={{ tx: 0.05, rx_in_bss: 0.07 }} />)
    expect(screen.getByRole('img', { name: 'Channel utilisation 12 %' })).toBeInTheDocument()
    expect(screen.getByText('Idle')).toBeInTheDocument()
  })
})

describe('ConfirmDialog', () => {
  it('needs the typed name before confirming', async () => {
    const onConfirm = vi.fn()
    render(<ConfirmDialog open onOpenChange={() => {}} title="Delete Hallway?" description="Gone." confirmLabel="Delete AP" destructive typeToConfirm="Hallway" onConfirm={onConfirm} />)
    const button = screen.getByRole('button', { name: 'Delete AP' })
    expect(button).toBeDisabled()
    await userEvent.type(screen.getByLabelText(/Type/), 'Hallway')
    expect(button).toBeEnabled()
    await userEvent.click(button)
    expect(onConfirm).toHaveBeenCalledOnce()
  })
})

describe('New site validation', () => {
  it('requires a unique name, a supported country and a timezone', () => {
    expect(validateSite({ name: '', country: '', timezone: 'Nowhere' }, [], ['UTC'])).toEqual({
      name: 'Enter a name.',
      country: 'Choose the country the APs are in.',
      timezone: 'Choose a timezone.',
    })
    expect(validateSite({ name: 'home', country: 'AU', timezone: 'UTC' }, ['Home'], ['UTC']).name).toMatch(/already/)
    expect(validateSite({ name: 'Lab', country: 'US', timezone: 'UTC' }, [], ['UTC']).country).toMatch(/not supported/)
    expect(validateSite({ name: 'Lab', country: 'AU', timezone: 'UTC' }, [], ['UTC'])).toEqual({})
  })
})
