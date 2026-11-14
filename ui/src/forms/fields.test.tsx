import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { z } from 'zod'
import type { ReactNode } from 'react'
import { ApiError } from '@/api/errors'
import { AppForm, useAppForm } from './Form'
import { NumberField } from './NumberField'
import { PasswordField, SSIDField, TextField } from './TextFields'
import { SwitchField } from './ChoiceFields'
import { useFormSubmit } from './submit'
import { numberInRange, passphraseOrKeep, ssid } from './validators'

function wrap(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

const schema = z.object({
  ssid,
  rssi: numberInRange(-95, -50, 'dBm').nullable(),
  offset: numberInRange(-10, 10).nullable(),
  psk: passphraseOrKeep,
  hidden: z.boolean(),
  led: z.number().nullable(),
})
type Values = z.input<typeof schema>

function Harness({ onSave, defaults }: { onSave: (values: z.output<typeof schema>) => Promise<unknown>; defaults?: Partial<Values> }) {
  const form = useAppForm(schema, { defaultValues: { ssid: 'Example', rssi: -72, offset: 0, psk: '', hidden: false, led: null, ...defaults } })
  const { submit, alert, staleDialog } = useFormSubmit({ form, mutationFn: (values) => onSave(values), objectName: 'WLAN Example' })
  return (
    <AppForm form={form} onSubmit={submit} alert={alert} id="f">
      <SSIDField name="ssid" label="Network name" />
      <NumberField name="rssi" label="Minimum signal" min={-95} max={-50} unit="dBm" />
      <NumberField name="offset" label="Offset" min={-10} max={10} />
      <PasswordField name="psk" label="Passphrase" stored />
      <SwitchField name="hidden" label="Hidden" />
      <TextField name="led" label="LED" inherit={{ value: 255 }} />
      <button type="submit">Save</button>
      {staleDialog}
    </AppForm>
  )
}

describe('forms kit', () => {
  it('negative-only numbers show the magnitude behind a fixed minus and store it negated', async () => {
    const onSave = vi.fn().mockResolvedValue({})
    render(wrap(<Harness onSave={onSave} />))
    const rssi = screen.getByLabelText('Minimum signal')
    expect(rssi).toHaveValue('72')
    expect(rssi).toHaveAttribute('inputmode', 'numeric')
    await userEvent.clear(rssi)
    await userEvent.type(rssi, '80')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls[0][0].rssi).toBe(-80)
  })

  it('signed numbers get a ± button and a text keypad', async () => {
    const onSave = vi.fn().mockResolvedValue({})
    render(wrap(<Harness onSave={onSave} />))
    const offset = screen.getByLabelText('Offset')
    expect(offset).toHaveAttribute('inputmode', 'text')
    await userEvent.clear(offset)
    await userEvent.type(offset, '4')
    await userEvent.click(screen.getByRole('button', { name: 'Change sign' }))
    expect(offset).toHaveValue('-4')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(onSave.mock.calls[0][0].offset).toBe(-4)
  })

  it('a failed submit shows an error summary and inline errors with aria wiring', async () => {
    render(wrap(<Harness onSave={vi.fn()} />))
    const name = screen.getByLabelText('Network name')
    await userEvent.clear(name)
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Fix 1 field before saving')).toBeInTheDocument()
    expect(name).toHaveAttribute('aria-invalid', 'true')
    const describedBy = name.getAttribute('aria-describedby')!
    expect(document.getElementById(describedBy.split(' ')[0])).toHaveTextContent('Enter a network name.')
  })

  it('write-only passphrases start as "Change passphrase" and use new-password', async () => {
    render(wrap(<Harness onSave={vi.fn()} />))
    expect(document.querySelector('input[autocomplete="new-password"]')).toBeNull()
    expect(screen.getByText('Set, not shown')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Change passphrase' }))
    const field = screen.getByLabelText('Passphrase')
    expect(field).toHaveAttribute('autocomplete', 'new-password')
    expect(field).toHaveAttribute('type', 'password')
    await userEvent.click(screen.getByRole('button', { name: 'Generate' }))
    expect((field as HTMLInputElement).value).toHaveLength(20)
  })

  it('maps 422 field errors onto fields and the rest into the form alert', async () => {
    const onSave = vi.fn().mockRejectedValue(
      new ApiError({ status: 422, code: 'invalid', message: 'Check the highlighted fields.', fields: { ssid: 'Already used by Guest.', 'radius.0.host': 'Unreachable.' } }),
    )
    render(wrap(<Harness onSave={onSave} />))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Already used by Guest.')).toBeInTheDocument()
    expect(screen.getByText('Unreachable.')).toBeInTheDocument()
    expect(screen.getByLabelText('Network name')).toHaveAttribute('aria-invalid', 'true')
  })

  it('409 stale offers Reload and Overwrite; Overwrite resends with the current revision', async () => {
    const onSave = vi
      .fn()
      .mockRejectedValueOnce(new ApiError({ status: 409, code: 'stale', message: 'Changed.', body: { object: { updated_rev: 9 } } }))
      .mockResolvedValueOnce({})
    function Overwrite() {
      const form = useAppForm(z.object({ name: z.string() }), { defaultValues: { name: 'a' } })
      const { submit, staleDialog } = useFormSubmit({ form, ifMatch: 7, mutationFn: (values, options) => onSave(values, options) })
      return (
        <AppForm form={form} onSubmit={submit}>
          <TextField name="name" label="Name" />
          <button type="submit">Save</button>
          {staleDialog}
        </AppForm>
      )
    }
    render(wrap(<Overwrite />))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('This changed since you opened it')).toBeInTheDocument()
    expect(onSave.mock.calls[0][1]).toEqual({ ifMatch: 7 })
    await userEvent.click(screen.getByRole('button', { name: 'Overwrite' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2))
    expect(onSave.mock.calls[1][1]).toEqual({ ifMatch: 9 })
  })

  it('inherited fields are locked until Override is switched on', async () => {
    render(wrap(<Harness onSave={vi.fn()} />))
    const led = screen.getByLabelText('LED')
    expect(led).toBeDisabled()
    expect(screen.getByText('255')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('switch', { name: 'Override' }))
    expect(led).toBeEnabled()
    expect(led).toHaveValue('255')
  })
})
