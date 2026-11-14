import { useId, useState, type ReactNode } from 'react'
import { SlidersHorizontal } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { inputClass } from '@/components/ui/input'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import { cn } from '@/lib/utils'

// Filters (design §7.7 rule 5): one row of selects at md and up; on phones a
// "Filters" button with a count chip opens a bottom sheet with Apply.

export type FilterOption = { value: string; label: string }
export type FilterDef = { id: string; label: string; value: string; options: FilterOption[]; allLabel?: string }

export function NativeSelect({
  label,
  value,
  options,
  onChange,
  allLabel = 'All',
  className,
  hideLabel = false,
}: {
  label: string
  value: string
  options: FilterOption[]
  onChange: (value: string) => void
  allLabel?: string | null
  className?: string
  hideLabel?: boolean
}) {
  const id = useId()
  return (
    <div className={cn('grid min-w-0 gap-1', className)}>
      <label htmlFor={id} className={cn('text-xs text-muted-foreground', hideLabel && 'sr-only')}>
        {label}
      </label>
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={cn(inputClass, 'pr-8')}>
        {allLabel !== null ? <option value="">{allLabel}</option> : null}
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </div>
  )
}

export function FilterBar({
  filters,
  onChange,
  onClear,
  extra,
}: {
  filters: FilterDef[]
  onChange: (values: Record<string, string>) => void
  onClear: () => void
  extra?: ReactNode
}) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const active = filters.filter((f) => f.value).length
  return (
    <div className="flex min-w-0 flex-wrap items-end gap-3">
      <div className="hidden min-w-0 flex-wrap items-end gap-3 md:flex">
        {filters.map((filter) => (
          <NativeSelect
            key={filter.id}
            label={filter.label}
            value={filter.value}
            options={filter.options}
            allLabel={filter.allLabel ?? 'All'}
            onChange={(value) => onChange({ [filter.id]: value })}
            className="w-40"
          />
        ))}
        {active ? (
          <Button variant="ghost" size="sm" onClick={onClear}>
            Clear filters
          </Button>
        ) : null}
      </div>
      <div className="md:hidden">
        <Button
          variant="outline"
          onClick={() => {
            setDraft(Object.fromEntries(filters.map((f) => [f.id, f.value])))
            setOpen(true)
          }}
        >
          <SlidersHorizontal />
          Filters
          {active ? (
            <span className="rounded-full bg-brand px-1.5 text-xs text-brand-foreground tabular-nums">{active}</span>
          ) : null}
        </Button>
        <Sheet open={open} onOpenChange={setOpen}>
          <SheetContent title="Filters">
            <div className="grid gap-4 px-3 pb-2">
              {filters.map((filter) => (
                <NativeSelect
                  key={filter.id}
                  label={filter.label}
                  value={draft[filter.id] ?? ''}
                  options={filter.options}
                  allLabel={filter.allLabel ?? 'All'}
                  onChange={(value) => setDraft((d) => ({ ...d, [filter.id]: value }))}
                />
              ))}
              <div className="grid grid-cols-2 gap-2 pt-2">
                <Button
                  variant="outline"
                  onClick={() => {
                    onClear()
                    setOpen(false)
                  }}
                >
                  Clear
                </Button>
                <Button
                  onClick={() => {
                    onChange(draft)
                    setOpen(false)
                  }}
                >
                  Apply
                </Button>
              </div>
            </div>
          </SheetContent>
        </Sheet>
      </div>
      {extra}
    </div>
  )
}
