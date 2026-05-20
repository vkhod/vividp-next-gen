import { CollapsibleSection, SectionBadge } from './CollapsibleSection'
import type { Classification } from '@/types/job'

interface ClassificationsPanelProps {
  classifications: Classification[]
}

export function ClassificationsPanel({ classifications }: ClassificationsPanelProps) {
  return (
    <CollapsibleSection
      title="Classifications"
      badge={<SectionBadge>{classifications.length}</SectionBadge>}
    >
      {classifications.length === 0 ? (
        <p className="text-xs text-muted-foreground py-1">No classification candidates yet.</p>
      ) : (
        <div className="space-y-2">
          {classifications.map((c) => (
            <div key={c.rank} className="flex items-center gap-3">
              <span className="text-xs text-muted-foreground w-4 text-right">#{c.rank}</span>
              <span className="text-xs font-medium flex-1">{c.name}</span>
              {/* Confidence bar */}
              <div className="flex items-center gap-2">
                <div className="w-20 h-1.5 rounded-full bg-muted overflow-hidden">
                  <div
                    className="h-full rounded-full bg-primary/60"
                    style={{ width: `${c.confidence}%` }}
                  />
                </div>
                <span className="text-xs tabular-nums text-muted-foreground w-8 text-right">
                  {c.confidence}%
                </span>
              </div>
            </div>
          ))}
        </div>
      )}
    </CollapsibleSection>
  )
}
