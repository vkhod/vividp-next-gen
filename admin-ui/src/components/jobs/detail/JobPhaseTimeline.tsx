import { formatDistanceStrict } from 'date-fns'
import { cn } from '@/lib/utils'
import type { JobDetail } from '@/types/job'

interface JobPhaseTimelineProps {
  job: JobDetail
}

interface Phase {
  label: string
  beganAt: string | null
  endedAt: string | null
}

function duration(from: string | null, to: string | null): string | null {
  if (!from || !to) return null
  try {
    return formatDistanceStrict(new Date(from), new Date(to))
  } catch {
    return null
  }
}

function shortTime(iso: string | null): string {
  if (!iso) return '—'
  try {
    return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  } catch {
    return '—'
  }
}

function PhaseSegment({
  phase,
  isFirst,
  isLast,
}: {
  phase: Phase
  isFirst: boolean
  isLast: boolean
}) {
  const dur = duration(phase.beganAt, phase.endedAt)
  const started = !!phase.beganAt
  const finished = !!phase.endedAt

  return (
    <div className="flex flex-col items-center min-w-0 flex-1">
      {/* Duration label above the line */}
      <span className={cn('text-xs mb-1 text-center px-1', dur ? 'text-muted-foreground' : 'text-muted-foreground/40')}>
        {dur ?? (started && !finished ? '…' : '—')}
      </span>

      {/* Dot + connecting lines */}
      <div className="flex items-center w-full">
        {!isFirst && (
          <div className={cn('h-px flex-1', finished ? 'bg-primary/40' : started ? 'bg-primary/20' : 'border-t border-dashed border-border')} />
        )}
        <div
          className={cn(
            'h-3 w-3 rounded-full border-2 shrink-0',
            finished
              ? 'bg-primary border-primary'
              : started
                ? 'bg-background border-primary'
                : 'bg-background border-border',
          )}
        />
        {!isLast && (
          <div className={cn('h-px flex-1', finished ? 'bg-primary/40' : 'border-t border-dashed border-border')} />
        )}
      </div>

      {/* Phase label */}
      <span className={cn('text-xs mt-1 text-center font-medium', started ? 'text-foreground' : 'text-muted-foreground/50')}>
        {phase.label}
      </span>

      {/* Start timestamp */}
      <span className="text-[10px] text-muted-foreground/70 text-center mt-0.5 tabular-nums">
        {shortTime(phase.beganAt)}
      </span>
    </div>
  )
}

export function JobPhaseTimeline({ job }: JobPhaseTimelineProps) {
  const phases: Phase[] = [
    { label: 'Capture', beganAt: job.capture_began_at, endedAt: job.capture_ended_at },
    { label: 'OCR', beganAt: job.ocr_began_at, endedAt: job.ocr_ended_at },
    { label: 'Verify', beganAt: job.verification_began_at, endedAt: job.verification_ended_at },
  ]

  const totalDur = duration(job.created_at, job.completed_at ?? null)

  return (
    <div className="px-4 py-4 border-b border-border/60">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-medium">Phase timeline</h3>
        {totalDur && (
          <span className="text-xs text-muted-foreground">Total: {totalDur}</span>
        )}
      </div>

      {/* Created → phases → completed */}
      <div className="flex items-start">
        {/* Created anchor */}
        <div className="flex flex-col items-center shrink-0 w-14">
          <span className="text-xs mb-1 text-muted-foreground/40">—</span>
          <div className="flex items-center w-full">
            <div className="h-3 w-3 rounded-full bg-muted border-2 border-border shrink-0 mx-auto" />
          </div>
          <span className="text-xs mt-1 text-muted-foreground/70 font-medium">Created</span>
          <span className="text-[10px] text-muted-foreground/60 tabular-nums">
            {shortTime(job.created_at)}
          </span>
        </div>

        {/* Connector from Created to first phase */}
        <div className={cn('h-px flex-1 mt-[22px]', job.capture_began_at ? 'bg-primary/40' : 'border-t border-dashed border-border')} />

        {/* Phase segments */}
        {phases.map((phase, i) => (
          <PhaseSegment
            key={phase.label}
            phase={phase}
            isFirst={false}
            isLast={i === phases.length - 1 && !job.completed_at}
          />
        ))}

        {/* Completed anchor (only when done) */}
        {job.completed_at && (
          <>
            <div className="h-px flex-1 mt-[22px] bg-primary/40" />
            <div className="flex flex-col items-center shrink-0 w-16">
              <span className="text-xs mb-1 text-muted-foreground/40">—</span>
              <div className="h-3 w-3 rounded-full bg-green-500 border-2 border-green-500 mx-auto" />
              <span className="text-xs mt-1 text-green-700 font-medium">Done</span>
              <span className="text-[10px] text-muted-foreground/60 tabular-nums">
                {shortTime(job.completed_at)}
              </span>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
