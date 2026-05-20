import { PauseCircle, PlayCircle, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { BulkAction } from '@/types/job'

interface BulkActionBarProps {
  count: number
  onHold: () => void
  onRelease: () => void
  onDelete: () => void
}

type HandlerKey = 'onHold' | 'onRelease' | 'onDelete'

const ACTIONS: {
  action: BulkAction
  label: string
  icon: React.ElementType
  variant: 'outline' | 'destructive'
  handler: HandlerKey
}[] = [
  { action: 'hold', label: 'Hold', icon: PauseCircle, variant: 'outline', handler: 'onHold' },
  { action: 'release', label: 'Release', icon: PlayCircle, variant: 'outline', handler: 'onRelease' },
  { action: 'delete', label: 'Delete', icon: Trash2, variant: 'destructive', handler: 'onDelete' },
]

export function BulkActionBar({ count, onHold, onRelease, onDelete }: BulkActionBarProps) {
  const handlers: Record<HandlerKey, () => void> = { onHold, onRelease, onDelete }

  return (
    <div className="fixed bottom-0 left-0 right-0 z-40 flex items-center justify-between border-t bg-background/95 px-6 py-3 shadow-lg backdrop-blur supports-[backdrop-filter]:bg-background/80">
      <span className="text-sm font-medium text-muted-foreground">
        <span className="text-foreground font-semibold">{count}</span>{' '}
        {count === 1 ? 'job' : 'jobs'} selected
      </span>

      <div className="flex items-center gap-2">
        {ACTIONS.map(({ action, label, icon: Icon, variant, handler }) => (
          <Button
            key={action}
            variant={variant}
            size="sm"
            onClick={handlers[handler]}
            className="gap-1.5"
          >
            <Icon className="h-4 w-4" />
            {label}
          </Button>
        ))}
      </div>
    </div>
  )
}
