import { CollapsibleSection } from './CollapsibleSection'

interface PipelineTimingsPanelProps {
  timings: Record<string, number>
}

const STATION_ORDER = ['ingest', 'classify', 'recognize', 'validate', 'verify', 'export']
const STATION_LABELS: Record<string, string> = {
  ingest: 'Ingestion',
  classify: 'Classification',
  recognize: 'Recognition',
  validate: 'Validation',
  verify: 'Verification',
  export: 'Export',
}

function formatMs(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

export function PipelineTimingsPanel({ timings }: PipelineTimingsPanelProps) {
  const entries = STATION_ORDER.filter((k) => k in timings).map((k) => ({
    key: k,
    label: STATION_LABELS[k] ?? k,
    ms: timings[k],
  }))

  const maxMs = Math.max(...entries.map((e) => e.ms), 1)

  if (entries.length === 0) {
    return (
      <CollapsibleSection title="Pipeline timings">
        <p className="text-xs text-muted-foreground py-1">No timing data yet.</p>
      </CollapsibleSection>
    )
  }

  return (
    <CollapsibleSection title="Pipeline timings">
      <div className="space-y-2">
        {entries.map((e) => (
          <div key={e.key} className="flex items-center gap-2 text-xs">
            <span className="w-24 text-muted-foreground shrink-0">{e.label}</span>
            <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
              <div
                className="h-full rounded-full bg-violet-400/70"
                style={{ width: `${(e.ms / maxMs) * 100}%` }}
              />
            </div>
            <span className="w-12 text-right tabular-nums text-muted-foreground">{formatMs(e.ms)}</span>
          </div>
        ))}
      </div>
    </CollapsibleSection>
  )
}
