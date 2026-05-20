import { useCallback } from 'react'
import { Search, X } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { JOB_STATUSES, DEFAULT_FILTERS } from '@/types/job'
import { MOCK_TENANTS, MOCK_SYSTEMS } from '@/data/mockTenants'
import type { JobFilters, JobStatus } from '@/types/job'

interface JobFilterBarProps {
  filters: JobFilters
  onChange: (filters: JobFilters) => void
}

const QUICK_FILTERS: { label: string; statuses: JobStatus[] }[] = [
  { label: 'Failures', statuses: ['FAILED', 'DEAD_LETTER'] },
  { label: 'On Hold', statuses: [] },
  { label: 'In Flight', statuses: ['INGESTING', 'CONVERTING', 'CLASSIFYING', 'RECOGNIZING', 'VALIDATING', 'VERIFYING', 'EXPORTING'] },
  { label: 'Needs Review', statuses: ['VALIDATION_FAILED', 'VERIFYING'] },
]

const STATUS_GROUP_LABELS: Partial<Record<JobStatus, string>> = {
  DETECTED: 'Detected',
  INGESTING: 'Ingesting',
  CONVERTING: 'Converting',
  INGESTED: 'Ingested',
  CLASSIFYING: 'Classifying',
  CLASSIFIED: 'Classified',
  RECOGNIZING: 'Recognizing',
  RECOGNIZED: 'Recognized',
  VALIDATING: 'Validating',
  VALIDATED: 'Validated',
  VALIDATION_FAILED: 'Val. Failed',
  VERIFYING: 'Verifying',
  VERIFIED: 'Verified',
  EXPORTING: 'Exporting',
  COMPLETED: 'Completed',
  FAILED: 'Failed',
  DEAD_LETTER: 'Dead Letter',
}

export function JobFilterBar({ filters, onChange }: JobFilterBarProps) {
  const tenantSystems = MOCK_SYSTEMS.filter(
    (s) => !filters.tenant_id || s.tenant_id === filters.tenant_id,
  )

  const isFiltered =
    filters.tenant_id ||
    filters.system_id ||
    filters.statuses.length > 0 ||
    filters.search ||
    filters.date_from ||
    filters.date_to

  const set = useCallback(
    (patch: Partial<JobFilters>) => onChange({ ...filters, ...patch }),
    [filters, onChange],
  )

  function toggleStatus(status: JobStatus) {
    const next = filters.statuses.includes(status)
      ? filters.statuses.filter((s) => s !== status)
      : [...filters.statuses, status]
    set({ statuses: next })
  }

  function applyQuick(idx: number) {
    const qf = QUICK_FILTERS[idx]
    const same =
      qf.statuses.length === filters.statuses.length &&
      qf.statuses.every((s) => filters.statuses.includes(s))
    set({ statuses: same ? [] : [...qf.statuses] })
  }

  function handleTenantChange(tenantId: string) {
    set({ tenant_id: tenantId, system_id: '' })
  }

  return (
    <div className="border-b bg-background px-6 py-3 space-y-3 shrink-0">
      {/* Row 1: dropdowns + date + search */}
      <div className="flex flex-wrap items-center gap-2">
        <select
          value={filters.tenant_id}
          onChange={(e) => handleTenantChange(e.target.value)}
          className="h-9 rounded-md border border-input bg-background px-3 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">All tenants</option>
          {MOCK_TENANTS.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>

        <select
          value={filters.system_id}
          onChange={(e) => set({ system_id: e.target.value })}
          disabled={!filters.tenant_id}
          className="h-9 rounded-md border border-input bg-background px-3 text-sm focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-50"
        >
          <option value="">All systems</option>
          {tenantSystems.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>

        <div className="flex items-center gap-1">
          <label className="text-xs text-muted-foreground whitespace-nowrap">From</label>
          <input
            type="date"
            value={filters.date_from}
            onChange={(e) => set({ date_from: e.target.value })}
            className="h-9 rounded-md border border-input bg-background px-3 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
        <div className="flex items-center gap-1">
          <label className="text-xs text-muted-foreground whitespace-nowrap">To</label>
          <input
            type="date"
            value={filters.date_to}
            onChange={(e) => set({ date_to: e.target.value })}
            className="h-9 rounded-md border border-input bg-background px-3 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>

        <div className="relative flex-1 min-w-48">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
          <Input
            placeholder="Search alias, filename, ID…"
            value={filters.search}
            onChange={(e) => set({ search: e.target.value })}
            className="pl-8"
          />
        </div>

        {isFiltered && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onChange({ ...DEFAULT_FILTERS })}
            className="gap-1 text-muted-foreground"
          >
            <X className="h-3.5 w-3.5" />
            Clear
          </Button>
        )}
      </div>

      {/* Row 2: status chips + quick filters */}
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-xs text-muted-foreground mr-1">Status:</span>
        {JOB_STATUSES.map((status) => (
          <button
            key={status}
            onClick={() => toggleStatus(status)}
            className={cn(
              'rounded-full border px-2 py-0.5 text-xs font-medium transition-colors',
              filters.statuses.includes(status)
                ? 'bg-primary text-primary-foreground border-primary'
                : 'border-input bg-background text-muted-foreground hover:text-foreground hover:border-foreground/30',
            )}
          >
            {STATUS_GROUP_LABELS[status]}
          </button>
        ))}

        <span className="ml-2 text-xs text-muted-foreground">Quick:</span>
        {QUICK_FILTERS.map((qf, idx) => {
          const active =
            qf.statuses.length > 0 &&
            qf.statuses.length === filters.statuses.length &&
            qf.statuses.every((s) => filters.statuses.includes(s))
          return (
            <button
              key={qf.label}
              onClick={() => applyQuick(idx)}
              className={cn(
                'rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors',
                active
                  ? 'bg-primary text-primary-foreground border-primary'
                  : 'border-dashed border-input bg-background text-muted-foreground hover:text-foreground hover:border-foreground/40',
              )}
            >
              {qf.label}
            </button>
          )
        })}
      </div>
    </div>
  )
}
