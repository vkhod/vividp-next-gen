import { Loader2 } from 'lucide-react'
import { useJobDetail } from '@/hooks/useJobDetail'
import { JobDetailHeader } from './detail/JobDetailHeader'
import { JobMetadataGrid } from './detail/JobMetadataGrid'
import { JobPhaseTimeline } from './detail/JobPhaseTimeline'
import { ClassificationsPanel } from './detail/ClassificationsPanel'
import { PipelineTimingsPanel } from './detail/PipelineTimingsPanel'
import { EventLogPanel } from './detail/EventLogPanel'
import { ArtifactsPanel } from './detail/ArtifactsPanel'
import { FieldsSummaryPanel } from './detail/FieldsSummaryPanel'
import type { BulkAction } from '@/types/job'

interface JobDetailPanelProps {
  jobId: string
  onClose: () => void
  onAction: (jobId: string, action: BulkAction) => void
}

export function JobDetailPanel({ jobId, onClose, onAction }: JobDetailPanelProps) {
  const { data: job, isLoading, error } = useJobDetail(jobId)

  if (isLoading) {
    return (
      <div className="flex flex-col h-full">
        <div className="border-b px-4 py-3 text-sm font-medium text-muted-foreground">
          Job detail
        </div>
        <div className="flex-1 flex items-center justify-center">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      </div>
    )
  }

  if (error || !job) {
    return (
      <div className="flex flex-col h-full">
        <div className="border-b px-4 py-3 text-sm font-medium text-muted-foreground">
          Job detail
        </div>
        <div className="flex-1 flex items-center justify-center p-6 text-center">
          <p className="text-sm text-red-600">Failed to load job details.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <JobDetailHeader
        job={job}
        onClose={onClose}
        onAction={(action) => onAction(job.id, action)}
      />

      <div className="flex-1 overflow-y-auto">
        <JobPhaseTimeline job={job} />
        <JobMetadataGrid job={job} />
        <ClassificationsPanel classifications={job.classifications} />
        <PipelineTimingsPanel timings={job.pipeline_timings} />
        <EventLogPanel jobId={job.id} />
        <ArtifactsPanel jobId={job.id} artifactCount={job.artifacts.length} />
        <FieldsSummaryPanel jobId={job.id} />
      </div>
    </div>
  )
}
