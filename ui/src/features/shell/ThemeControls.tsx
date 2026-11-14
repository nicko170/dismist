import { Monitor, Moon, Sun } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'
import { useTheme, type ThemeChoice } from './theme'

export const themeOptions: { value: ThemeChoice; label: string; icon: typeof Sun }[] = [
  { value: 'system', label: 'System', icon: Monitor },
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
]

/** Sidebar footer menu (desktop) and rail button (tablet). */
export function ThemeMenu({ className, showLabel }: { className?: string; showLabel?: boolean }) {
  const { theme, resolved, setTheme } = useTheme()
  const Icon = theme === 'system' ? Monitor : resolved === 'dark' ? Moon : Sun
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size={showLabel ? 'sm' : 'icon-sm'} className={cn('text-muted-foreground hover:text-foreground', className)} aria-label="Theme">
          <Icon />
          {showLabel ? <span>Theme</span> : null}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start">
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(value) => setTheme(value as ThemeChoice)}>
          {themeOptions.map((option) => (
            <DropdownMenuRadioItem key={option.value} value={option.value}>
              <option.icon />
              {option.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** Three-way segmented control for the More sheet. */
export function ThemeSegmented() {
  const { theme, setTheme } = useTheme()
  return (
    <div role="radiogroup" aria-label="Theme" className="grid grid-cols-3 gap-1 rounded-lg bg-muted p-1">
      {themeOptions.map((option) => (
        <button
          key={option.value}
          type="button"
          role="radio"
          aria-checked={theme === option.value}
          onClick={() => setTheme(option.value)}
          className="flex h-11 items-center justify-center gap-2 rounded-md text-sm text-muted-foreground transition-colors aria-checked:bg-card aria-checked:font-medium aria-checked:text-foreground aria-checked:shadow-sm"
        >
          <option.icon className="size-4" aria-hidden />
          {option.label}
        </button>
      ))}
    </div>
  )
}
