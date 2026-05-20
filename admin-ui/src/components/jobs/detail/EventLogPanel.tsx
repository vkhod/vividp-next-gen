import { formatDistanceToNow } from 'date-fns'
import { ArrowRight, Loader2 } from 'lucide-react'
import { CollapsibleSection, SectionBadge } from './CollapsibleSection'
import { useJobTransitions } from '@/hooks/useJobDetail'

interface EventLogPanelProps {
  jobId: string
}

export function EventLogPanel({ jobId }: EventLogPanelProps) {
  const { data: transitions = [], isLoading } = useJobTransitions(jobId)

  return (
    <CollapsibleSection
      title="Event log"
      badge={!isLoading ? <SectionBadge>{transitions.length}</SectionBadge> : undefined}
    >
      {isLoading ? (
        <div className="flex items-center gap-2 py-2 text-xs text-muted-foreground">
          <Loader2 className="h-3 w-3 animate-spin" />
          Loading…
        </div>
      ) : transitions.length === 0 ? (
        <p className="text-xs text-muted-foreground py-1">No transitions recorded yet.</p>
      ) : (
        <div className="space-y-1">
          {[...transitions].reverse().map((t) => (
            <div key={t.id} className="text-xs border-b border-border/30 last:border-0 py-1.5">
              <div className="flex items-center gap-1 flex-wrap">
                {t.from_status ? (
                  <>
                    <span className="text-muted-foreground font-mono">{t.from_status}</span>
                    <ArrowRight className="h-3 w-3 text-muted-foreground shrink-0" />
                  </>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
                <span className="font-medium font-mono">{t.to_status}</span>
              </div>
              <div className="flex items-center gap-2 mt-0.5 text-muted-foreground">
                {t.worker_id && <span className="font-mono">{t.worker_id}</span>}
                <span
                  title={t.occurred_at}
                  className="ml-auto"
                >
                  {formatDistanceToNow(new Date(t.occurred_at), { addSuffix: true })}
                </span>
              </div>
              {t.note && (
                <p className="mt-1 text-muted-foreground/80 italic line-clamp-2">{t.note}</p>
              )}
            </div>
          ))}
        </div>
      )}
    </CollapsibleSection>
  )
}
