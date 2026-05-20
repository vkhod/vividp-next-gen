import { MOCK_JOBS } from './mockJobs'
import type {
  Job,
  JobDetail,
  JobTransition,
  FieldsSummary,
  JobStatus,
  ArtifactWithUrl,
} from '@/types/job'

const STATUS_ORDER: JobStatus[] = [
  'DETECTED', 'INGESTING', 'CONVERTING', 'INGESTED',
  'CLASSIFYING', 'CLASSIFIED',
  'RECOGNIZING', 'RECOGNIZED', 'VALIDATING', 'VALIDATED',
  'VERIFICATION_FAILED' as JobStatus, // handled below
  'VERIFYING', 'VERIFIED', 'EXPORTING', 'COMPLETED',
]

function statusIndex(s: JobStatus): number {
  if (s === 'VALIDATION_FAILED') return STATUS_ORDER.indexOf('VALIDATING') + 1
  if (s === 'FAILED' || s === 'DEAD_LETTER') return 999
  return STATUS_ORDER.indexOf(s)
}

function stepsUpTo(job: Job): JobStatus[] {
  if (job.status === 'DEAD_LETTER' || job.status === 'FAILED') {
    // Include everything up to the stage where failure was recorded
    const stageStart: Record<string, JobStatus> = {
      INGESTION: 'DETECTED',
      CLASSIFICATION: 'INGESTED',
      RECOGNITION: 'CLASSIFIED',
      VALIDATION: 'RECOGNIZED',
      VERIFICATION: 'VALIDATED',
      EXPORT: 'VERIFIED',
    }
    const lastGood = stageStart[job.stage] ?? 'DETECTED'
    const cutoff = statusIndex(lastGood)
    const chain = STATUS_ORDER.filter((s) => statusIndex(s) <= cutoff)
    return [...chain, job.status]
  }
  const cutoff = statusIndex(job.status)
  return STATUS_ORDER.filter((s) => statusIndex(s) <= cutoff)
}

function addMs(iso: string, ms: number): string {
  return new Date(Date.parse(iso) + ms).toISOString()
}

const WORKER_FOR_STATUS: Partial<Record<string, string>> = {
  INGESTING: 'worker-ingest-01',
  CONVERTING: 'worker-ingest-01',
  CLASSIFYING: 'worker-classify-01',
  RECOGNIZING: 'worker-ocr-02',
  VALIDATING: 'worker-validate-01',
  VERIFYING: 'operator-cohen',
  EXPORTING: 'worker-export-01',
}

export function getMockTransitions(job: Job): JobTransition[] {
  const chain = stepsUpTo(job)
  const base = Date.parse(job.created_at)
  const stepMs = [0, 5000, 15000, 35000, 45000, 60000, 75000, 90000,
                  105000, 180000, 210000, 240000, 265000, 280000, 370000, 450000, 510000]

  return chain.map((to_status, i) => ({
    id: i + 1,
    job_id: job.id,
    from_status: i === 0 ? null : chain[i - 1],
    to_status,
    worker_id: WORKER_FOR_STATUS[to_status] ?? null,
    note:
      to_status === 'FAILED'
        ? job.last_error ?? null
        : to_status === 'VALIDATION_FAILED'
          ? 'Validation rule math_balance failed'
          : null,
    occurred_at: new Date(base + (stepMs[i] ?? i * 30000)).toISOString(),
  }))
}

export function getMockArtifacts(job: Job): ArtifactWithUrl[] {
  const base = `jobs/${job.tenant_id.slice(0, 8)}/${job.id}/`
  const t = job.created_at
  const artifacts: ArtifactWithUrl[] = []

  if (statusIndex(job.status) >= statusIndex('INGESTED')) {
    artifacts.push({
      key: `${base}original/${job.source_filename}`,
      type: 'original',
      size_bytes: job.size_bytes,
      created_at: addMs(t, 3000),
      presigned_url: null, // TODO (Phase 3): MinIO presign
    })
    const pages = job.page_count ?? 1
    for (let p = 1; p <= Math.min(pages, 3); p++) {
      artifacts.push({
        key: `${base}pages/${String(p).padStart(3, '0')}/original.tif`,
        type: 'original_tif',
        size_bytes: Math.round(job.size_bytes / pages),
        created_at: addMs(t, 5000 + p * 500),
        presigned_url: null,
      })
    }
  }

  if (statusIndex(job.status) >= statusIndex('CLASSIFIED')) {
    const pages = job.page_count ?? 1
    for (let p = 1; p <= Math.min(pages, 3); p++) {
      artifacts.push({
        key: `${base}pages/${String(p).padStart(3, '0')}/registered.tif`,
        type: 'registered_tif',
        size_bytes: Math.round(job.size_bytes / pages),
        created_at: addMs(t, 90000 + p * 500),
        presigned_url: null,
      })
    }
  }

  if (statusIndex(job.status) >= statusIndex('COMPLETED')) {
    artifacts.push({
      key: `${base}export/data.xml`,
      type: 'export_data',
      size_bytes: 4096,
      created_at: addMs(t, 500000),
      presigned_url: null,
    })
  }

  return artifacts
}

export function getMockFieldsSummary(job: Job): FieldsSummary {
  const total = (job.page_count ?? 1) * 8
  const recognized = Math.round(total * 0.92)
  const validated = Math.round(recognized * 0.85)
  const corrected = Math.round(validated * 0.12)

  return {
    total,
    recognized,
    validated,
    operator_corrected: corrected,
    avg_confidence:
      statusIndex(job.status) >= statusIndex('RECOGNIZED') ? 78.4 : null,
    by_state: {
      recognized: recognized - validated,
      validated,
      not_validated: total - recognized,
    },
    by_source: {
      ocr: Math.round(validated * 0.6),
      llm: Math.round(validated * 0.28),
      operator: corrected,
      default: validated - Math.round(validated * 0.6) - Math.round(validated * 0.28) - corrected,
    },
  }
}

function phaseTimestamps(job: Job) {
  const t = job.created_at
  const idx = statusIndex(job.status)

  return {
    capture_began_at: idx >= statusIndex('INGESTING') ? addMs(t, 1000) : null,
    capture_ended_at: idx >= statusIndex('INGESTED') ? addMs(t, 35000) : null,
    ocr_began_at: idx >= statusIndex('RECOGNIZING') ? addMs(t, 105000) : null,
    ocr_ended_at: idx >= statusIndex('RECOGNIZED') ? addMs(t, 180000) : null,
    verification_began_at: idx >= statusIndex('VERIFYING') ? addMs(t, 280000) : null,
    verification_ended_at: idx >= statusIndex('VERIFIED') ? addMs(t, 370000) : null,
  }
}

export function getMockJobDetail(id: string): JobDetail | null {
  const job = MOCK_JOBS.find((j) => j.id === id)
  if (!job) return null

  const idx = statusIndex(job.status)
  const phases = phaseTimestamps(job)

  const pipelineTiming: Record<string, number> = {}
  if (idx >= statusIndex('INGESTED')) pipelineTiming.ingest = 420
  if (idx >= statusIndex('CLASSIFIED')) pipelineTiming.classify = 850
  if (idx >= statusIndex('RECOGNIZED')) pipelineTiming.recognize = 1240
  if (idx >= statusIndex('VALIDATED')) pipelineTiming.validate = 320
  if (idx >= statusIndex('VERIFIED')) pipelineTiming.verify = 5400
  if (idx >= statusIndex('COMPLETED')) pipelineTiming.export = 580

  return {
    ...job,
    ...phases,
    verifier_name: idx >= statusIndex('VERIFIED') ? 'operator-cohen' : null,
    verification_seconds: idx >= statusIndex('VERIFIED') ? 432 : null,
    keystrokes_count: idx >= statusIndex('VERIFIED') ? 187 : null,
    capture_type: 1,
    metadata_override: false,
    suspected_fields_count: (job.page_count ?? 1) * 8,
    content_hash:
      idx >= statusIndex('COMPLETED')
        ? 'sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
        : null,
    hash_computed_at: idx >= statusIndex('COMPLETED') ? job.completed_at : null,
    tif_file_key:
      idx >= statusIndex('INGESTED')
        ? `jobs/${job.tenant_id.slice(0, 8)}/${job.id}/pages/001/original.tif`
        : null,
    tif_reg_file_key:
      idx >= statusIndex('CLASSIFIED')
        ? `jobs/${job.tenant_id.slice(0, 8)}/${job.id}/pages/001/registered.tif`
        : null,
    filter: null,
    pipeline_timings: pipelineTiming,
    classifications:
      job.top_classification
        ? [
            { rank: 1, ...job.top_classification, type: 1 },
            { rank: 2, name: 'Unknown', confidence: Math.max(0, job.top_classification.confidence - 30), type: 0 },
          ]
        : [],
    artifacts: getMockArtifacts(job),
    job_state: {},
  }
}
