import { X, PauseCircle, PlayCircle, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/components/jobs/StatusBadge'
import type { JobDetail } from '@/types/job'
import type { BulkAction } from '@/types/job'

interface JobDetailHeaderProps {
  job: JobDetail
  onClose: () => void
  onAction: (action: BulkAction) => void
}

export function JobDetailHeader({ job, onClose, onAction }: JobDetailHeaderProps) {
  return (
    <div className="border-b px-4 py-3 shrink-0 space-y-2">
      {/* Top row: title + close */}
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="font-semibold truncate">
            {job.job_alias ?? (
              <span className="text-muted-foreground font-normal italic">no alias</span>
            )}
          </h2>
          <p className="text-xs text-muted-foreground font-mono mt-0.5 truncate">
            {job.tenant_name} › {job.system_name}
          </p>
        </div>
        <Button variant="ghost" size="icon" onClick={onClose} className="h-7 w-7 shrink-0">
          <X className="h-4 w-4" />
        </Button>
      </div>

      {/* Status + actions row */}
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <StatusBadge status={job.status} />
          <span className="text-xs text-muted-foreground">{job.stage}</span>
        </div>
        <div className="flex items-center gap-1">
          {job.on_hold ? (
            <Button variant="outline" size="sm" onClick={() => onAction('release')} className="h-7 gap-1 text-xs">
              <PlayCircle className="h-3.5 w-3.5" />
              Release
            </Button>
          ) : (
            <Button variant="outline" size="sm" onClick={() => onAction('hold')} className="h-7 gap-1 text-xs">
              <PauseCircle className="h-3.5 w-3.5" />
              Hold
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            onClick={() => onAction('delete')}
            className="h-7 gap-1 text-xs text-destructive hover:text-destructive hover:border-destructive"
          >
            <Trash2 className="h-3.5 w-3.5" />
            Delete
          </Button>
        </div>
      </div>
    </div>
  )
}
