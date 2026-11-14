import { Button } from '@/components/ui/button'

/** The ± key of a signed NumberField: the iOS digit pad has no minus sign. */
export function PlusMinus({ onClick, disabled }: { onClick: () => void; disabled?: boolean }) {
  return (
    <Button variant="outline" size="icon" aria-label="Change sign" disabled={disabled} onClick={onClick} className="text-base">
      ±
    </Button>
  )
}
