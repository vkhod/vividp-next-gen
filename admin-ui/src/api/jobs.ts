import type { Job, JobDetail, JobTransition, ArtifactWithUrl, FieldsSummary, BulkAction } from '@/types/job'

const BASE = import.meta.env.VITE_ADMIN_API_URL ?? ''

function url(path: string, params?: Record<string, string | string[] | undefined>) {
  const u = BASE ? new URL(path, BASE) : new URL(path, window.location.origin)
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v === undefined || v === '') continue
      if (Array.isArray(v)) {
        v.forEach((s) => u.searchParams.append(k, s))
      } else {
        u.searchParams.set(k, v)
      }
    }
  }
  return u.toString()
}

async function apiFetch<T>(input: string, init?: RequestInit): Promise<T> {
  const res = await fetch(input, init)
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${res.status} ${res.statusText}${body ? `: ${body}` : ''}`)
  }
  // 204 No Content — return undefined cast to T
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

// ── List ──────────────────────────────────────────────────────────────────────

export interface ListJobsParams {
  tenant_id?: string
  system_id?: string
  status?: string[]
  date_from?: string
  date_to?: string
  search?: string
  sort?: string
  dir?: 'asc' | 'desc'
}

export async function fetchJobs(params: ListJobsParams): Promise<Job[]> {
  const data = await apiFetch<{ jobs: Job[] }>(url('/api/admin/jobs', {
    tenant_id: params.tenant_id,
    system_id: params.system_id,
    status: params.status,
    date_from: params.date_from,
    date_to: params.date_to,
    search: params.search,
    sort: params.sort,
    dir: params.dir,
  }))
  return data.jobs
}

// ── Detail ────────────────────────────────────────────────────────────────────

export function fetchJobDetail(id: string): Promise<JobDetail> {
  return apiFetch(url(`/api/admin/jobs/${id}`))
}

export function fetchJobTransitions(id: string): Promise<JobTransition[]> {
  return apiFetch(url(`/api/admin/jobs/${id}/transitions`))
}

export function fetchJobArtifacts(id: string): Promise<ArtifactWithUrl[]> {
  return apiFetch(url(`/api/admin/jobs/${id}/artifacts`))
}

export function fetchFieldsSummary(id: string): Promise<FieldsSummary> {
  return apiFetch(url(`/api/admin/jobs/${id}/fields/summary`))
}

// ── Mutations ─────────────────────────────────────────────────────────────────

export function holdJob(id: string): Promise<void> {
  return apiFetch(url(`/api/admin/jobs/${id}/hold`), { method: 'POST' })
}

export function releaseJob(id: string): Promise<void> {
  return apiFetch(url(`/api/admin/jobs/${id}/release`), { method: 'POST' })
}

export function deleteJob(id: string): Promise<void> {
  return apiFetch(url(`/api/admin/jobs/${id}`), { method: 'DELETE' })
}

export function callAction(id: string, action: BulkAction): Promise<void> {
  if (action === 'hold') return holdJob(id)
  if (action === 'release') return releaseJob(id)
  return deleteJob(id)
}
