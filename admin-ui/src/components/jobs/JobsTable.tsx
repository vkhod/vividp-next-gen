import { formatDistanceToNow } from 'date-fns'
import { AlertCircle, Lock, RefreshCw } from 'lucide-react'
import { Checkbox } from '@/components/ui/checkbox'
import { StatusBadge } from './StatusBadge'
import { cn } from '@/lib/utils'
import type { Job } from '@/types/job'

interface JobsTableProps {
  jobs: Job[]
  isLoading: boolean
  selected: Set<string>
  onSelectAll: (checked: boolean) => void
  onSelectOne: (id: string, checked: boolean) => void
  onRowClick: (id: string) => void
  activeJobId: string | null
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function RelativeTime({ iso }: { iso: string }) {
  try {
    return (
      <span title={iso}>
        {formatDistanceToNow(new Date(iso), { addSuffix: true })}
      </span>
    )
  } catch {
    return <span className="text-muted-foreground">—</span>
  }
}

export function JobsTable({
  jobs,
  isLoading,
  selected,
  onSelectAll,
  onSelectOne,
  onRowClick,
  activeJobId,
}: JobsTableProps) {
  const allSelected = jobs.length > 0 && jobs.every((j) => selected.has(j.id))
  const someSelected = jobs.some((j) => selected.has(j.id)) && !allSelected

  return (
    <div className="overflow-auto h-full">
      <table className="w-full text-sm border-separate border-spacing-0">
        <thead className="sticky top-0 z-10 bg-muted/80 backdrop-blur">
          <tr>
            <th className="w-10 px-3 py-2.5 text-left font-medium">
              <Checkbox
                checked={allSelected}
                data-state={someSelected ? 'indeterminate' : allSelected ? 'checked' : 'unchecked'}
                onCheckedChange={(checked) => onSelectAll(!!checked)}
                aria-label="Select all"
              />
            </th>
            <th className="px-3 py-2.5 text-left font-medium text-muted-foreground">Job</th>
            <th className="px-3 py-2.5 text-left font-medium text-muted-foreground w-44">Status</th>
            <th className="px-3 py-2.5 text-left font-medium text-muted-foreground w-20">Pri</th>
            <th className="px-3 py-2.5 text-left font-medium text-muted-foreground w-24">Files/Pg</th>
            <th className="px-3 py-2.5 text-left font-medium text-muted-foreground w-28">Size</th>
            <th className="px-3 py-2.5 text-left font-medium text-muted-foreground w-32">Created</th>
            <th className="px-3 py-2.5 text-left font-medium text-muted-foreground w-32">Worker</th>
            <th className="w-10 px-3 py-2.5" />
          </tr>
        </thead>

        <tbody>
          {isLoading && (
            <tr>
              <td colSpan={9} className="px-4 py-12 text-center text-muted-foreground text-sm">
                Loading…
              </td>
            </tr>
          )}

          {!isLoading && jobs.length === 0 && (
            <tr>
              <td colSpan={9} className="px-4 py-12 text-center text-muted-foreground text-sm">
                No jobs match the current filters.
              </td>
            </tr>
          )}

          {jobs.map((job) => (
            <JobRow
              key={job.id}
              job={job}
              isSelected={selected.has(job.id)}
              isActive={job.id === activeJobId}
              onSelect={(checked) => onSelectOne(job.id, checked)}
              onClick={() => onRowClick(job.id)}
            />
          ))}
        </tbody>
      </table>
    </div>
  )
}

interface JobRowProps {
  job: Job
  isSelected: boolean
  isActive: boolean
  onSelect: (checked: boolean) => void
  onClick: () => void
}

function JobRow({ job, isSelected, isActive, onSelect, onClick }: JobRowProps) {
  return (
    <tr
      onClick={onClick}
      className={cn(
        'border-b border-border/50 cursor-pointer transition-colors',
        isActive ? 'bg-accent' : isSelected ? 'bg-accent/40 hover:bg-accent/60' : 'hover:bg-muted/40',
      )}
    >
      {/* Checkbox */}
      <td className="w-10 px-3 py-2.5" onClick={(e) => e.stopPropagation()}>
        <Checkbox
          checked={isSelected}
          onCheckedChange={(checked) => onSelect(!!checked)}
          aria-label={`Select ${job.job_alias ?? job.id}`}
        />
      </td>

      {/* Job identity */}
      <td className="px-3 py-2.5 max-w-0">
        <div className="flex items-center gap-1.5 truncate">
          {job.on_hold && (
            <span title="On hold">
              <Lock className="h-3 w-3 text-amber-500 shrink-0" />
            </span>
          )}
          <div className="min-w-0">
            <p className="font-medium truncate">
              {job.job_alias ?? (
                <span className="text-muted-foreground italic text-xs">no alias</span>
              )}
            </p>
            <p className="text-xs text-muted-foreground font-mono truncate">{job.id.slice(0, 8)}…</p>
          </div>
        </div>
      </td>

      {/* Status */}
      <td className="px-3 py-2.5 w-44">
        <div className="flex flex-col gap-0.5">
          <StatusBadge status={job.status} />
          <span className="text-xs text-muted-foreground">{job.stage}</span>
        </div>
      </td>

      {/* Priority */}
      <td className="px-3 py-2.5 w-20">
        <div className="flex items-center gap-1">
          {job.priority > 0 ? (
            <span className="inline-flex items-center rounded-full bg-blue-50 border border-blue-200 px-2 py-0.5 text-xs font-semibold text-blue-700">
              {job.priority}
            </span>
          ) : (
            <span className="text-muted-foreground text-xs">—</span>
          )}
          {job.retry_count > 0 && (
            <span
              title={`${job.retry_count} retr${job.retry_count === 1 ? 'y' : 'ies'}`}
              className="inline-flex items-center gap-0.5 text-xs text-amber-600"
            >
              <RefreshCw className="h-3 w-3" />
              {job.retry_count}
            </span>
          )}
        </div>
      </td>

      {/* Files / pages */}
      <td className="px-3 py-2.5 w-24 text-sm tabular-nums">
        <span>{job.nfiles}</span>
        <span className="text-muted-foreground mx-1">/</span>
        <span>{job.page_count ?? '—'}</span>
      </td>

      {/* Size */}
      <td className="px-3 py-2.5 w-28 text-sm text-muted-foreground tabular-nums">
        {formatBytes(job.size_bytes)}
      </td>

      {/* Created */}
      <td className="px-3 py-2.5 w-32 text-sm text-muted-foreground">
        <RelativeTime iso={job.created_at} />
      </td>

      {/* Worker */}
      <td className="px-3 py-2.5 w-32 text-xs text-muted-foreground truncate">
        {job.claimed_by ? (
          <span title={job.claimed_by}>{job.claimed_by}</span>
        ) : (
          '—'
        )}
      </td>

      {/* Error indicator */}
      <td className="w-10 px-3 py-2.5 text-right">
        {job.last_error && (
          <span title={job.last_error}>
            <AlertCircle className="h-4 w-4 text-red-500 inline" />
          </span>
        )}
      </td>
    </tr>
  )
}
