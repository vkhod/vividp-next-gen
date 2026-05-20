import { useState } from 'react'
import { Loader2 } from 'lucide-react'
import { CollapsibleSection } from './CollapsibleSection'
import { useJobFieldsSummary } from '@/hooks/useJobDetail'

interface FieldsSummaryPanelProps {
  jobId: string
}

function StatBox({ label, value, sub }: { label: string; value: string | number; sub?: string }) {
  return (
    <div className="rounded border border-border/50 bg-muted/30 px-3 py-2 text-center">
      <p className="text-base font-semibold tabular-nums">{value}</p>
      <p className="text-[10px] text-muted-foreground leading-tight mt-0.5">{label}</p>
      {sub && <p className="text-[10px] text-muted-foreground/60">{sub}</p>}
    </div>
  )
}

export function FieldsSummaryPanel({ jobId }: FieldsSummaryPanelProps) {
  const [enabled, setEnabled] = useState(false)
  const { data, isLoading } = useJobFieldsSummary(jobId, enabled)

  // Trigger the lazy fetch when the section first opens
  function handleOpen() {
    setEnabled(true)
  }

  return (
    <CollapsibleSection title="Fields summary" defaultOpen={false}>
      {/* Fire the query lazily when this section opens */}
      <div onMouseEnter={handleOpen} onClick={handleOpen}>
        {isLoading || (!data && !enabled) ? (
          <div
            className="flex items-center gap-2 py-2 text-xs text-muted-foreground cursor-pointer"
            onClick={handleOpen}
          >
            {isLoading ? (
              <>
                <Loader2 className="h-3 w-3 animate-spin" />
                Loading…
              </>
            ) : (
              'Click to load field summary'
            )}
          </div>
        ) : data ? (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-2">
              <StatBox label="Total fields" value={data.total} />
              <StatBox label="Recognized" value={data.recognized} sub={`${Math.round((data.recognized / data.total) * 100)}%`} />
              <StatBox label="Validated" value={data.validated} />
              <StatBox label="Op. corrected" value={data.operator_corrected} />
            </div>
            {data.avg_confidence != null && (
              <div>
                <div className="flex justify-between text-xs mb-1">
                  <span className="text-muted-foreground">Avg OCR confidence</span>
                  <span className="tabular-nums font-medium">{data.avg_confidence.toFixed(1)}%</span>
                </div>
                <div className="h-1.5 rounded-full bg-muted overflow-hidden">
                  <div
                    className="h-full rounded-full bg-sky-400/70"
                    style={{ width: `${data.avg_confidence}%` }}
                  />
                </div>
              </div>
            )}
            <div className="text-xs space-y-1">
              <p className="text-muted-foreground font-medium mb-1">By source</p>
              {Object.entries(data.by_source)
                .filter(([, v]) => v > 0)
                .map(([k, v]) => (
                  <div key={k} className="flex justify-between">
                    <span className="text-muted-foreground capitalize">{k}</span>
                    <span className="tabular-nums">{v}</span>
                  </div>
                ))}
            </div>
          </div>
        ) : null}
      </div>
    </CollapsibleSection>
  )
}
