import { useState } from 'react'
import { CheckCircle, SkipForward, AlertTriangle, Loader2 } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { StatusBadge } from './StatusBadge'
import { callAction } from '@/api/jobs'
import type { Job, BulkAction } from '@/types/job'

interface BulkConfirmDialogProps {
  jobs: Job[]
  action: BulkAction
  onComplete: (results: ActionResult[]) => void
  onCancel: () => void
}

export interface ActionResult {
  jobId: string
  jobAlias: string | null
  status: 'confirmed' | 'skipped' | 'failed'
  error?: string
}

const ACTION_LABELS: Record<BulkAction, { verb: string; buttonLabel: string; destructive: boolean }> = {
  hold: { verb: 'hold', buttonLabel: 'Hold job', destructive: false },
  release: { verb: 'release', buttonLabel: 'Release job', destructive: false },
  delete: { verb: 'delete', buttonLabel: 'Delete job', destructive: true },
}

export function BulkConfirmDialog({ jobs, action, onComplete, onCancel }: BulkConfirmDialogProps) {
  const [index, setIndex] = useState(0)
  const [results, setResults] = useState<ActionResult[]>([])
  const [executing, setExecuting] = useState(false)
  const [execError, setExecError] = useState<string | null>(null)

  const current = jobs[index]
  const meta = ACTION_LABELS[action]
  const remaining = jobs.length - index
  const isLast = index === jobs.length - 1

  function advance(next: ActionResult) {
    const updated = [...results, next]
    if (isLast) {
      onComplete(updated)
    } else {
      setResults(updated)
      setIndex((i) => i + 1)
      setExecError(null)
    }
  }

  async function confirm() {
    setExecuting(true)
    setExecError(null)
    try {
      await callAction(current.id, action)
      advance({ jobId: current.id, jobAlias: current.job_alias, status: 'confirmed' })
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Action failed'
      setExecError(msg)
    } finally {
      setExecuting(false)
    }
  }

  function skip() {
    advance({ jobId: current.id, jobAlias: current.job_alias, status: 'skipped' })
  }

  if (!current) return null

  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 capitalize">
            {meta.destructive && <AlertTriangle className="h-4 w-4 text-destructive" />}
            {meta.verb.charAt(0).toUpperCase() + meta.verb.slice(1)} job
          </DialogTitle>
          <DialogDescription>
            Job{' '}
            <span className="font-semibold text-foreground">
              {index + 1} of {jobs.length}
            </span>{' '}
            — {remaining - 1} remaining after this
          </DialogDescription>
        </DialogHeader>

        <div className="rounded-md border bg-muted/40 p-4 space-y-2">
          <div className="flex items-start justify-between gap-2">
            <div>
              <p className="font-medium text-sm">
                {current.job_alias ?? <span className="text-muted-foreground italic">no alias</span>}
              </p>
              <p className="text-xs text-muted-foreground font-mono mt-0.5">
                {current.id.slice(0, 8)}…
              </p>
            </div>
            <StatusBadge status={current.status} />
          </div>
          <p className="text-xs text-muted-foreground truncate">{current.source_filename}</p>
          {current.last_error && (
            <p className="text-xs text-red-600 bg-red-50 rounded px-2 py-1 line-clamp-2">
              {current.last_error}
            </p>
          )}
          {execError && (
            <p className="text-xs text-red-600 bg-red-50 rounded px-2 py-1">
              Error: {execError}
            </p>
          )}
        </div>

        <DialogFooter className="gap-2">
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={executing}>
            Cancel all
          </Button>
          <Button variant="outline" size="sm" onClick={skip} disabled={executing} className="gap-1.5">
            <SkipForward className="h-3.5 w-3.5" />
            Skip
          </Button>
          <Button
            variant={meta.destructive ? 'destructive' : 'default'}
            size="sm"
            onClick={confirm}
            disabled={executing}
            className="gap-1.5"
          >
            {executing
              ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
              : <CheckCircle className="h-3.5 w-3.5" />
            }
            {isLast ? `${meta.buttonLabel} & finish` : meta.buttonLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
