import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm, FormProvider } from 'react-hook-form'
import { z } from 'zod'
import type { MutationResponse } from '@/gen/api.gen'
import { ApiError } from '@/api/errors'
import { SecurityField } from '@/features/wlans/fields'
import { ConfigForm } from './ConfigForm'
import { ConfigFormProvider, type ConfigFormContextValue } from './context'
import { NumberInput, OptionField } from './fields'
import { useConfigForm, type SubmitFn } from './useConfigForm'

// jsdom has no ResizeObserver; Radix measures the switch thumb with it.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const context: ConfigFormContextValue = { siteId: 'home', experimentalAllowed: false, readOnly: false, legacyEditable: null, expert: null, expertLink: null }

type Values = { hidden: boolean; dtim: number | string | null }

function response(object: Values & { updated_rev: number }, summary: string): MutationResponse<Values & { updated_rev: number }> {
  return {
    revision: 48,
    object,
    impact: { aps: [{ id: '02-aa-00-00-00-01', name: 'Hallway', connected: true, push: 'reconnect', disruption: ['wlan_restart'] }], disruption: 'wlan_restart', summary },
    warnings: [],
  }
}

function Harness({ submit, ctx = context }: { submit: SubmitFn<Values & { updated_rev: number }>; ctx?: ConfigFormContextValue }) {
  const [client] = useState(() => new QueryClient())
  return (
    <QueryClientProvider client={client}>
      <Form submit={submit} ctx={ctx} />
    </QueryClientProvider>
  )
}

function Form({ submit, ctx }: { submit: SubmitFn<Values & { updated_rev: number }>; ctx: ConfigFormContextValue }) {
  const controller = useConfigForm<Values, Values & { updated_rev: number }>({
    formKey: 'test:wlan',
    scope: 'wlan',
    values: { hidden: false, dtim: 2 },
    schema: z.object({ hidden: z.boolean(), dtim: z.number({ error: 'Enter a whole number from 1 to 255.' }).int().min(1).max(255) }),
    submit,
    ifMatch: 5,
    toValues: ({ hidden, dtim }) => ({ hidden, dtim }),
  })
  return (
    <ConfigForm controller={controller} context={ctx}>
      <OptionField k="wlan.hidden" />
      <OptionField k="wlan.dtim" />
    </ConfigForm>
  )
}

describe('config form', () => {
  it('dry-runs a change, shows the impact line, then saves with If-Match', async () => {
    const user = userEvent.setup()
    const submit = vi.fn<SubmitFn<Values & { updated_rev: number }>>(async (_body, options) =>
      response({ hidden: true, dtim: 2, updated_rev: options.dryRun ? 5 : 6 }, 'WLAN Example restarts on 1 AP; about 15 clients reconnect'),
    )
    render(<Harness submit={submit} />)
    expect(screen.queryByRole('region', { name: 'Save changes' })).toBeNull()
    await user.click(screen.getByRole('switch', { name: 'Hidden SSID' }))
    const bar = await screen.findByRole('region', { name: 'Save changes' })
    expect(bar).toHaveTextContent('1 change')
    await waitFor(() => expect(bar).toHaveTextContent('WLAN Example restarts on 1 AP'), { timeout: 3000 })
    expect(submit).toHaveBeenCalledWith({ hidden: true }, expect.objectContaining({ dryRun: true, ifMatch: 5 }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(submit).toHaveBeenLastCalledWith({ hidden: true }, { ifMatch: 5 }))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Save changes' })).toBeNull())
  })

  it('shows field errors from the bounds and from the server', async () => {
    const user = userEvent.setup()
    const submit = vi.fn<SubmitFn<Values & { updated_rev: number }>>(async (_body, options) => {
      if (options.dryRun) return response({ hidden: false, dtim: 7, updated_rev: 5 }, '')
      throw new ApiError({ status: 422, code: 'firmware_rule', message: 'The AP would reject this config.', fields: { 'wlan.dtim': 'DTIM 7 is not allowed with WMM off.' } })
    })
    render(<Harness submit={submit} />)
    const dtim = screen.getByLabelText('DTIM period')
    await user.clear(dtim)
    await user.type(dtim, 'x')
    await user.tab()
    expect(await screen.findByText('Enter a whole number from 1 to 255.')).toBeInTheDocument()
    expect(dtim).toHaveAttribute('aria-invalid', 'true')
    await user.clear(dtim)
    await user.type(dtim, '7')
    await waitFor(() => expect(screen.queryByText('Enter a whole number from 1 to 255.')).toBeNull())
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('DTIM 7 is not allowed with WMM off.')).toBeInTheDocument()
    // Mapped onto its field, so no form-level Alert repeats it (§7.4 "Errors").
    expect(screen.getAllByRole('alert').some((a) => a.textContent?.includes('The AP would reject this config.'))).toBe(false)
  })

  it('offers Reload or Overwrite on 409 stale', async () => {
    const user = userEvent.setup()
    let calls = 0
    const submit = vi.fn<SubmitFn<Values & { updated_rev: number }>>(async (_body, options) => {
      if (options.dryRun) return response({ hidden: true, dtim: 2, updated_rev: 5 }, '')
      calls++
      if (calls === 1) throw new ApiError({ status: 409, code: 'stale', message: 'This changed since you opened it.', body: { current: { hidden: false, dtim: 3, updated_rev: 9 } } })
      return response({ hidden: true, dtim: 3, updated_rev: 10 }, '')
    })
    render(<Harness submit={submit} />)
    await user.click(screen.getByRole('switch', { name: 'Hidden SSID' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alertdialog', { name: 'This changed since you opened it' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Overwrite' }))
    await waitFor(() => expect(submit).toHaveBeenLastCalledWith({ hidden: true }, { ifMatch: 9 }))
  })
})

describe('fields', () => {
  it('hides experimental security values unless the site allows them', () => {
    function Security({ allowed }: { allowed: boolean }) {
      const form = useForm({ defaultValues: { security: 'wpa2_wpa3_personal' } })
      return (
        <ConfigFormProvider value={{ ...context, experimentalAllowed: allowed }}>
          <FormProvider {...form}>
            <SecurityField />
          </FormProvider>
        </ConfigFormProvider>
      )
    }
    const { unmount } = render(<Security allowed={false} />)
    expect(screen.queryByRole('radio', { name: /Enhanced Open/ })).toBeNull()
    expect(screen.getByRole('radio', { name: /WPA2-Enterprise/ })).toBeInTheDocument()
    unmount()
    render(<Security allowed />)
    expect(screen.getByRole('radio', { name: /Enhanced Open \(OWE\)/ })).toHaveTextContent('Experimental')
  })

  it('hides an unused experimental option and locks a stored one', () => {
    function Rates({ value }: { value: string }) {
      const form = useForm({ defaultValues: { qos_class: value } })
      return (
        <ConfigFormProvider value={context}>
          <FormProvider {...form}>
            <OptionField k="wlan.qos_class" />
          </FormProvider>
        </ConfigFormProvider>
      )
    }
    const { unmount } = render(<Rates value="none" />)
    expect(screen.queryByLabelText('QoS override')).toBeNull()
    unmount()
    render(<Rates value="voice" />)
    expect(screen.getByLabelText(/QoS override/)).toBeDisabled()
    expect(screen.getByText(/Turn on experimental options in Settings › Advanced/)).toBeInTheDocument()
  })

  it('enters negative-only numbers as a magnitude behind a fixed minus', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    function Wrapper() {
      const [value, setValue] = useState<number | string | null>(null)
      return (
        <NumberInput
          id="rssi"
          aria-label="Minimum signal"
          value={value}
          onChange={(v) => {
            setValue(v)
            onChange(v)
          }}
          min={-95}
          max={-50}
        />
      )
    }
    render(<Wrapper />)
    const input = screen.getByLabelText('Minimum signal')
    expect(input).toHaveAttribute('inputmode', 'numeric')
    expect(screen.getByText('−')).toBeInTheDocument()
    await user.type(input, '75')
    expect(onChange).toHaveBeenLastCalledWith(-75)
  })
})
