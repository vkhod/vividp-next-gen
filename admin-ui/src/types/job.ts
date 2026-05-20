export const JOB_STATUSES = [
  'DETECTED',
  'INGESTING',
  'CONVERTING',
  'INGESTED',
  'CLASSIFYING',
  'CLASSIFIED',
  'RECOGNIZING',
  'RECOGNIZED',
  'VALIDATING',
  'VALIDATED',
  'VALIDATION_FAILED',
  'VERIFYING',
  'VERIFIED',
  'EXPORTING',
  'COMPLETED',
  'FAILED',
  'DEAD_LETTER',
] as const

export type JobStatus = (typeof JOB_STATUSES)[number]

export interface Job {
  id: string
  tenant_id: string
  tenant_name: string
  system_id: string
  system_name: string
  job_name: string | null
  job_alias: string | null
  status: JobStatus
  stage: string
  priority: number
  on_hold: boolean
  on_error: 0 | 1 | 2
  retry_count: number
  nfiles: number
  page_count: number | null
  scanned_pages: number | null
  source_filename: string
  source_bucket: string
  source_key: string
  size_bytes: number
  user_data: string | null
  claimed_by: string | null
  claimed_at: string | null
  last_error: string | null
  error_comment: string | null
  is_duplicate: boolean
  is_archived: boolean
  skipped_verify: boolean
  skipped_trutypist: boolean
  created_at: string
  updated_at: string
  completed_at: string | null
  top_classification: { name: string; confidence: number } | null
}

export interface Tenant {
  id: string
  name: string
  slug: string
}

export interface SystemRef {
  id: string
  tenant_id: string
  name: string
}

export interface JobFilters {
  tenant_id: string
  system_id: string
  statuses: JobStatus[]
  search: string
  date_from: string
  date_to: string
}

export const DEFAULT_FILTERS: JobFilters = {
  tenant_id: '',
  system_id: '',
  statuses: [],
  search: '',
  date_from: '',
  date_to: '',
}

export type BulkAction = 'hold' | 'release' | 'delete'

export const ON_ERROR_LABELS: Record<0 | 1 | 2, string> = {
  0: 'Retry',
  1: 'Skip',
  2: 'Escalate',
}

// ── Detail-view types ────────────────────────────────────────────────────────

export interface Classification {
  rank: number
  name: string
  type: number
  confidence: number
}

export interface Artifact {
  key: string
  type: string
  size_bytes: number
  created_at: string
}

export interface ArtifactWithUrl extends Artifact {
  // TODO (Phase 3): presigned GET URL from MinIO, TTL 15 min
  presigned_url: string | null
}

export interface JobTransition {
  id: number
  job_id: string
  from_status: string | null
  to_status: string
  worker_id: string | null
  note: string | null
  occurred_at: string
}

export interface FieldsSummary {
  total: number
  recognized: number
  validated: number
  operator_corrected: number
  avg_confidence: number | null
  by_state: Record<string, number>
  by_source: Record<string, number>
}

/** Extended job record — fetched individually via GET /api/admin/jobs/:id */
export interface JobDetail extends Job {
  // Phase timestamps (six independent pairs from the DB)
  capture_began_at: string | null
  capture_ended_at: string | null
  ocr_began_at: string | null
  ocr_ended_at: string | null
  verification_began_at: string | null
  verification_ended_at: string | null
  // Operator metrics
  verifier_name: string | null
  verification_seconds: number | null
  keystrokes_count: number | null
  // Additional scalars omitted from list view
  capture_type: number | null
  metadata_override: boolean
  suspected_fields_count: number | null
  content_hash: string | null
  hash_computed_at: string | null
  tif_file_key: string | null
  tif_reg_file_key: string | null
  filter: string | null
  // JSONB blobs
  pipeline_timings: Record<string, number>
  classifications: Classification[]
  artifacts: Artifact[]
  job_state: Record<string, unknown>
}
