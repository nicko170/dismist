import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { EventView } from '@/gen/api.gen'
import { EventList } from './EventList'

const event = (partial: Partial<EventView>): EventView => ({
  id: 'e1',
  site_id: 'home',
  ap_id: '02-aa-00-00-00-01',
  ap_name: 'Hallway',
  kind: 'dns',
  pace_type: 3,
  severity: 'warning',
  title: 'DNS queries failing',
  detail: 'DNS queries failing',
  client_mac: '',
  wlan_id: '',
  first_seen: new Date().toISOString(),
  last_seen: new Date().toISOString(),
  count: 12,
  resolved_by: null,
  data: {},
  ...partial,
})

describe('EventList', () => {
  it('hides a detail equal to the title, shows the AP, the count and the kind', async () => {
    const onOpen = vi.fn()
    render(<EventList events={[event({})]} label="Events" onOpen={onOpen} />)
    expect(screen.getAllByText('DNS queries failing')).toHaveLength(1)
    expect(screen.getByText('×12')).toBeInTheDocument()
    expect(screen.getByText('Hallway')).toBeInTheDocument()
    expect(screen.getByText('Warning')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button'))
    expect(onOpen).toHaveBeenCalledOnce()
  })
  it('greys resolved events and says who resolved them', () => {
    render(<EventList events={[event({ resolved_by: 'ev-9', detail: 'More' })]} label="Events" />)
    expect(screen.getByText('resolved by ev-9')).toBeInTheDocument()
    expect(screen.getByText('More')).toBeInTheDocument()
    expect(screen.getByText('Resolved')).toBeInTheDocument()
  })
})
