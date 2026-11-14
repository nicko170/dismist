import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { APDetail, RadioView } from '@/gen/api.gen'
import { samplesQuery } from '@/api/queries/aps'
import { Chip } from '@/components/app/StatusChip'
import { formatCount } from '@/lib/format'
import { Card, Dash, Facts } from '@/features/overview/kit/Card'
import { Sparkline } from '@/features/overview/kit/TimeSeries'
import { UtilBar } from '@/features/overview/kit/UtilBar'
import { orderRadios, phyName, powerText, radioTitle } from './format'
import { radioMismatch } from './APOverviewTab'
import { APNeighbours } from './NeighbourList'

// AP detail Radios tab (design §3.5): one card per radio, the scanning radio
// labelled as such; utilisation as a stacked bar with text; a 24 h trend per
// radio from dismist's own samples (no Prometheus needed).

const counterLabels: Record<string, string> = {
  tx_retries: 'Tx retries',
  tx_failed: 'Tx failed',
  rx_decrypt_failures: 'Rx decrypt failures',
  reinits: 'Re-inits',
  beacons_per_s: 'Beacons/s',
}

function configuredSource(ap: APDetail, radio: RadioView): string {
  if (radio.role === 'scan' || !radio.band) return 'Not configurable'
  const override = ap.overrides?.rf?.[radio.band]
  if (override && (override.channel || override.width || override.power_dbm)) {
    const parts = [override.channel ? `ch ${override.channel}` : '', override.width ? `${override.width} MHz` : '', override.power_dbm ? `${override.power_dbm} dBm` : '']
    return `AP override · static (${parts.filter(Boolean).join(', ')})`
  }
  const plan = ap.rf_plan?.[radio.band]
  if (plan) return `Site profile · ${plan.mode === 'auto' ? 'planner' : 'static'} (ch ${plan.channel}/${plan.width}, ${plan.power_dbm} dBm)`
  if (ap.config.shadowed?.some((p) => p.toLowerCase().startsWith('radios'))) return 'Expert patch'
  return 'Site profile'
}

function RadioCard({ ap, radio, trend }: { ap: APDetail; radio: RadioView; trend: { channel: (number | null)[]; util: (number | null)[]; clients: (number | null)[] } | null }) {
  const scan = radio.role === 'scan'
  const counters = Object.entries(radio.counters ?? {}).filter(([, v]) => v)
  const mismatch = radioMismatch(ap, radio)
  return (
    <Card
      title={radioTitle(radio)}
      description={scan ? 'Scanning radio, used for neighbour and utilisation data' : undefined}
      action={mismatch ? <Chip tone="warn">Differs from plan: {mismatch}</Chip> : null}
    >
      <div className="grid gap-4">
        <Facts
          items={[
            {
              label: 'Channel',
              value: radio.channel ? (
                <span className="tabular-nums">
                  {radio.channel}
                  {radio.secondary ? ` (+${radio.secondary})` : ''} · {radio.width || 20} MHz
                </span>
              ) : (
                <Dash />
              ),
            },
            { label: 'PHY', value: phyName(radio.phy), hidden: scan && !radio.phy },
            { label: 'Tx power', value: powerText(radio), hidden: scan },
            { label: 'Noise floor', value: radio.noise_dbm == null ? <Dash /> : `${radio.noise_dbm} dBm` },
            { label: 'Clients', value: radio.clients, hidden: scan },
            {
              label: 'DFS',
              value: radio.dfs ? (
                <span>
                  {radio.dfs.cac || 'No CAC'}
                  {radio.dfs.radar_detected ? <Chip tone="crit" className="ml-2">Radar detected</Chip> : null}
                </span>
              ) : null,
              hidden: !radio.dfs,
            },
            {
              label: 'Configured',
              value: scan ? (
                'Not configurable'
              ) : (
                <span>
                  {configuredSource(ap, radio)}{' '}
                  <Link
                    to={'/sites/$siteId/aps/$apId/settings' as never}
                    params={{ siteId: ap.site_id, apId: ap.id } as never}
                    hash="radios"
                    className="text-brand hover:underline"
                  >
                    Edit
                  </Link>
                </span>
              ),
            },
          ]}
        />
        <div className="grid gap-1.5">
          <h3 className="text-xs font-medium text-muted-foreground">Channel utilisation</h3>
          <UtilBar util={radio.util} parts={radio.util_parts} />
        </div>
        {counters.length ? (
          <div className="grid gap-1.5">
            <h3 className="text-xs font-medium text-muted-foreground">Counters (per minute)</h3>
            <ul className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
              {counters.map(([key, value]) => (
                <li key={key}>
                  {counterLabels[key] ?? key.replace(/_/g, ' ')} <span className="tabular-nums font-medium">{formatCount(value)}</span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {trend ? (
          <div className="grid gap-1.5">
            <h3 className="text-xs font-medium text-muted-foreground">Last 24 h</h3>
            <div className="flex flex-wrap gap-x-6 gap-y-2 text-xs text-muted-foreground">
              <span className="inline-flex items-center gap-2">
                Utilisation <Sparkline values={trend.util} label={`${radioTitle(radio)} utilisation, last 24 hours`} />
              </span>
              {!scan ? (
                <span className="inline-flex items-center gap-2">
                  Clients <Sparkline values={trend.clients} label={`${radioTitle(radio)} clients, last 24 hours`} />
                </span>
              ) : null}
              <span className="inline-flex items-center gap-2">
                Channel{' '}
                <span className="tabular-nums text-foreground">
                  {[...new Set(trend.channel.filter((c): c is number => Boolean(c)))].join(' → ') || '–'}
                </span>
              </span>
            </div>
          </div>
        ) : null}
      </div>
    </Card>
  )
}

export function APRadiosTab({ ap, siteId }: { ap: APDetail; siteId: string }) {
  const samples = useQuery({ ...samplesQuery(siteId, { ap: ap.id, range: '24h' }), retry: false })
  const radios = orderRadios(ap.radios_detail)
  const trends = useMemo(() => {
    const rows = (samples.data ?? []).filter((s) => !s.ap_id || s.ap_id === ap.id)
    if (rows.length < 2) return null
    const byBand = (band: string | null) => {
      const key = band ?? 'scan'
      const pick = (s: (typeof rows)[number]) => s.radios.find((r) => (r.band || 'scan') === key || (key === 'scan' && r.band === ''))
      return {
        channel: rows.map((s) => pick(s)?.channel ?? null),
        util: rows.map((s) => pick(s)?.util ?? null),
        clients: rows.map((s) => pick(s)?.clients ?? null),
      }
    }
    return byBand
  }, [samples.data, ap.id])

  return (
    <div className="grid min-w-0 gap-4">
      {radios.length ? (
        <div className="grid min-w-0 gap-4 lg:grid-cols-2">
          {radios.map((radio) => (
            <RadioCard key={radio.dev} ap={ap} radio={radio} trend={trends ? trends(radio.role === 'scan' ? null : radio.band) : null} />
          ))}
        </div>
      ) : (
        <p className="rounded-lg border border-dashed px-5 py-8 text-sm text-muted-foreground">
          {ap.connected ? 'Waiting for the AP’s first full report (sent every minute).' : 'No radio data: the AP is not connected.'}
        </p>
      )}
      <Card title="Neighbouring networks" description="Heard by this AP’s scanning radio.">
        <APNeighbours siteId={siteId} apId={ap.id} />
      </Card>
    </div>
  )
}
