import { Copy } from 'lucide-react'
import { CollapsibleSection } from './CollapsibleSection'
import { ON_ERROR_LABELS } from '@/types/job'
import type { JobDetail } from '@/types/job'

interface JobMetadataGridProps {
  job: JobDetail
}

function Row({ label, value, mono = false }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-2 py-1.5 border-b border-border/30 last:border-0 text-xs">
      <span className="text-muted-foreground truncate">{label}</span>
      <span className={mono ? 'font-mono break-all' : 'break-words'}>{value ?? '—'}</span>
    </div>
  )
}

function CopyableId({ value }: { value: string }) {
  function copy() {
    void navigator.clipboard.writeText(value)
  }
  return (
    <span className="inline-flex items-center gap-1 group">
      <span className="font-mono">{value}</span>
      <button onClick={copy} className="opacity-0 group-hover:opacity-100 transition-opacity">
        <Copy className="h-3 w-3 text-muted-foreground" />
      </button>
    </span>
  )
}

function formatBytes(b: number) {
  if (b < 1024) return `${b} B`
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(0)} KB`
  return `${(b / (1024 * 1024)).toFixed(1)} MB`
}

export function JobMetadataGrid({ job }: JobMetadataGridProps) {
  return (
    <div>
      <CollapsibleSection title="Identity" defaultOpen>
        <Row label="Job ID" value={<CopyableId value={job.id} />} />
        <Row label="Job name" value={job.job_name} mono />
        <Row label="Job alias" value={job.job_alias} />
        <Row label="Tenant" value={job.tenant_name} />
        <Row label="System" value={job.system_name} />
      </CollapsibleSection>

      <CollapsibleSection title="Status" defaultOpen>
        <Row label="Status" value={job.status} />
        <Row label="Stage" value={job.stage} />
        <Row label="On hold" value={job.on_hold ? 'Yes' : 'No'} />
        <Row label="Error mode" value={ON_ERROR_LABELS[job.on_error]} />
        <Row label="Retry count" value={String(job.retry_count)} />
        <Row label="Claimed by" value={job.claimed_by} mono />
        <Row label="Claimed at" value={job.claimed_at ? new Date(job.claimed_at).toLocaleString() : null} />
        <Row label="Verifier" value={job.verifier_name} />
        <Row label="Verify time" value={job.verification_seconds != null ? `${job.verification_seconds}s` : null} />
        <Row label="Keystrokes" value={job.keystrokes_count != null ? String(job.keystrokes_count) : null} />
      </CollapsibleSection>

      <CollapsibleSection title="Source">
        <Row label="Filename" value={job.source_filename} mono />
        <Row label="Bucket" value={job.source_bucket} mono />
        <Row label="Source key" value={job.source_key} mono />
        <Row label="Size" value={formatBytes(job.size_bytes)} />
        <Row label="Files" value={String(job.nfiles)} />
        <Row label="Capture type" value={job.capture_type != null ? String(job.capture_type) : null} />
        <Row label="Meta override" value={job.metadata_override ? 'Yes' : 'No'} />
        <Row label="User data" value={job.user_data} />
        <Row label="Filter" value={job.filter} />
      </CollapsibleSection>

      <CollapsibleSection title="Pages">
        <Row label="Page count" value={job.page_count != null ? String(job.page_count) : null} />
        <Row label="Scanned pages" value={job.scanned_pages != null ? String(job.scanned_pages) : null} />
        <Row label="Suspected fields" value={job.suspected_fields_count != null ? String(job.suspected_fields_count) : null} />
        <Row label="TIF key" value={job.tif_file_key} mono />
        <Row label="Registered TIF" value={job.tif_reg_file_key} mono />
      </CollapsibleSection>

      {(job.last_error || job.error_comment) && (
        <CollapsibleSection title="Error" defaultOpen>
          {job.last_error && (
            <div className="rounded bg-red-50 border border-red-200 px-3 py-2 text-xs text-red-800 font-mono break-all mb-2">
              {job.last_error}
            </div>
          )}
          {job.error_comment && (
            <p className="text-xs text-muted-foreground">{job.error_comment}</p>
          )}
        </CollapsibleSection>
      )}

      <CollapsibleSection title="Flags">
        <Row label="Skip verify" value={job.skipped_verify ? 'Yes' : 'No'} />
        <Row label="Skip typist" value={job.skipped_trutypist ? 'Yes' : 'No'} />
        <Row label="Duplicate" value={job.is_duplicate ? 'Yes' : 'No'} />
        <Row label="Archived" value={job.is_archived ? 'Yes' : 'No'} />
      </CollapsibleSection>

      <CollapsibleSection title="Integrity">
        <Row label="Priority" value={String(job.priority)} />
        <Row label="Content hash" value={job.content_hash ? <CopyableId value={job.content_hash.slice(0, 20) + '…'} /> : null} />
        <Row label="Hash at" value={job.hash_computed_at ? new Date(job.hash_computed_at).toLocaleString() : null} />
        <Row label="Created" value={new Date(job.created_at).toLocaleString()} />
        <Row label="Updated" value={new Date(job.updated_at).toLocaleString()} />
        <Row label="Completed" value={job.completed_at ? new Date(job.completed_at).toLocaleString() : null} />
      </CollapsibleSection>
    </div>
  )
}
