import { cn } from '@/lib/utils'
import type { JobStatus } from '@/types/job'

interface StatusBadgeProps {
  status: JobStatus
  className?: string
}

const STATUS_STYLES: Record<JobStatus, string> = {
  DETECTED: 'bg-slate-100 text-slate-700 border-slate-200',
  INGESTING: 'bg-blue-50 text-blue-700 border-blue-200',
  CONVERTING: 'bg-blue-50 text-blue-700 border-blue-200',
  INGESTED: 'bg-sky-50 text-sky-700 border-sky-200',
  CLASSIFYING: 'bg-violet-50 text-violet-700 border-violet-200',
  CLASSIFIED: 'bg-sky-50 text-sky-700 border-sky-200',
  RECOGNIZING: 'bg-violet-50 text-violet-700 border-violet-200',
  RECOGNIZED: 'bg-sky-50 text-sky-700 border-sky-200',
  VALIDATING: 'bg-violet-50 text-violet-700 border-violet-200',
  VALIDATED: 'bg-sky-50 text-sky-700 border-sky-200',
  VALIDATION_FAILED: 'bg-amber-50 text-amber-700 border-amber-200',
  VERIFYING: 'bg-indigo-50 text-indigo-700 border-indigo-200',
  VERIFIED: 'bg-sky-50 text-sky-700 border-sky-200',
  EXPORTING: 'bg-violet-50 text-violet-700 border-violet-200',
  COMPLETED: 'bg-green-50 text-green-700 border-green-200',
  FAILED: 'bg-red-50 text-red-700 border-red-200',
  DEAD_LETTER: 'bg-red-100 text-red-800 border-red-300',
}

const ACTIVE_STATUSES = new Set<JobStatus>([
  'INGESTING', 'CONVERTING', 'CLASSIFYING',
  'RECOGNIZING', 'VALIDATING', 'VERIFYING', 'EXPORTING',
])

export function StatusBadge({ status, className }: StatusBadgeProps) {
  const isActive = ACTIVE_STATUSES.has(status)

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium',
        STATUS_STYLES[status],
        className,
      )}
    >
      {isActive && (
        <span className="relative flex h-1.5 w-1.5">
          <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-current opacity-75" />
          <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-current" />
        </span>
      )}
      {status.replace(/_/g, '​_')}
    </span>
  )
}
