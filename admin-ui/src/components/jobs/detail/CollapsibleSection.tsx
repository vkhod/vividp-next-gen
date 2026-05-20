import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'

interface CollapsibleSectionProps {
  title: string
  badge?: React.ReactNode
  defaultOpen?: boolean
  onOpenChange?: (open: boolean) => void
  children: React.ReactNode
  className?: string
}

export function CollapsibleSection({
  title,
  badge,
  defaultOpen = false,
  onOpenChange,
  children,
  className,
}: CollapsibleSectionProps) {
  const [open, setOpen] = useState(defaultOpen)

  function handleToggle() {
    const next = !open
    setOpen(next)
    onOpenChange?.(next)
  }

  return (
    <div className={cn('border-b border-border/60', className)}>
      <button
        onClick={handleToggle}
        className="flex w-full items-center justify-between px-4 py-3 text-left hover:bg-muted/40 transition-colors"
      >
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium">{title}</span>
          {badge}
        </div>
        <ChevronDown
          className={cn('h-4 w-4 text-muted-foreground transition-transform duration-150', open && 'rotate-180')}
        />
      </button>
      {open && <div className="px-4 pb-4">{children}</div>}
    </div>
  )
}

export function SectionBadge({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full bg-muted border border-border px-1.5 py-0.5 text-xs text-muted-foreground">
      {children}
    </span>
  )
}
