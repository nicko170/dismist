import { useState } from 'react'
import { FileDiff } from 'lucide-react'
import type { FieldValues, UseFormReturn } from 'react-hook-form'
import type { Impact, Warning } from '@/api/types'
import { Button } from '@/components/ui/button'
import { ImpactDialog } from '@/components/app/ImpactDialog'
import { ImpactNotice } from '@/components/app/ImpactNotice'
import { StickyActionBar } from '@/components/app/StickyActionBar'
import { countDirty } from './Form'

// The save bar of a typed config form: changed-field count, the dry-run
// impact line, "View diff" (the per-AP redacted diff), Discard and Save
// (design §3.0 "Saving", §7.4 "Impact preview").

export function FormSaveBar<TIn extends FieldValues, TOut extends FieldValues>({
  form,
  formId,
  dryRun,
  pending = false,
  blocked = false,
  saveLabel = 'Save',
  title = 'Impact of saving',
}: {
  form: UseFormReturn<TIn, unknown, TOut>
  formId: string
  dryRun?: { impact: Impact | null; warnings: readonly Warning[]; loading: boolean; error: unknown }
  pending?: boolean
  /** Save disabled (an experimental acknowledgement is missing). */
  blocked?: boolean
  saveLabel?: string
  title?: string
}) {
  const [preview, setPreview] = useState(false)
  const { isDirty, dirtyFields, isValid, errors } = form.formState
  const invalid = !isValid && Object.keys(errors).length > 0
  return (
    <>
      <StickyActionBar
        open={isDirty}
        changed={countDirty(dirtyFields)}
        form={formId}
        pending={pending}
        disabled={blocked}
        saveLabel={saveLabel}
        onDiscard={() => form.reset()}
        impact={
          dryRun ? (
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <ImpactNotice impact={dryRun.impact} loading={dryRun.loading} error={dryRun.error} invalid={invalid} />
              {dryRun.impact?.aps.length ? (
                <Button variant="link" size="sm" className="h-auto min-h-11 px-0 fine:md:min-h-0" onClick={() => setPreview(true)}>
                  <FileDiff aria-hidden /> View diff
                </Button>
              ) : null}
            </span>
          ) : undefined
        }
      />
      {dryRun ? (
        <ImpactDialog open={preview} onOpenChange={setPreview} title={title} impact={dryRun.impact} warnings={dryRun.warnings} loading={dryRun.loading} />
      ) : null}
    </>
  )
}
