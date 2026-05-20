import { useState, useMemo, useCallback, useRef, useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { JobFilterBar } from '@/components/jobs/JobFilterBar'
import { JobsTable } from '@/components/jobs/JobsTable'
import { BulkActionBar } from '@/components/jobs/BulkActionBar'
import { BulkConfirmDialog } from '@/components/jobs/BulkConfirmDialog'
import { JobDetailPanel } from '@/components/jobs/JobDetailPanel'
import { useJobs } from '@/hooks/useJobs'
import { DEFAULT_FILTERS } from '@/types/job'
import type { JobFilters, BulkAction, Job } from '@/types/job'

const PANEL_MIN = 280
const PANEL_MAX = 800
const PANEL_DEFAULT = 420

export function JobsAdminPage() {
  const queryClient = useQueryClient()
  const [filters, setFilters] = useState<JobFilters>(DEFAULT_FILTERS)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [detailJobId, setDetailJobId] = useState<string | null>(null)
  const [panelWidth, setPanelWidth] = useState(PANEL_DEFAULT)

  // Pending action: set directly from detail panel (single job) or bulk bar (multi)
  const [pendingAction, setPendingAction] = useState<{
    jobs: Job[]
    action: BulkAction
  } | null>(null)

  const { jobs, isLoading } = useJobs(filters)

  // Close detail panel if the active job is no longer in the filtered list
  useEffect(() => {
    if (detailJobId !== null && !jobs.some((j) => j.id === detailJobId)) {
      setDetailJobId(null)
    }
  }, [jobs, detailJobId])

  const selectedJobs = useMemo(
    () => jobs.filter((j) => selected.has(j.id)),
    [jobs, selected],
  )

  const handleSelectAll = useCallback(
    (checked: boolean) => {
      setSelected(checked ? new Set(jobs.map((j) => j.id)) : new Set())
    },
    [jobs],
  )

  const handleSelectOne = useCallback((id: string, checked: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }, [])

  function handleRowClick(id: string) {
    setDetailJobId((prev) => (prev === id ? null : id))
  }

  function handleBulkTrigger(action: BulkAction) {
    setPendingAction({ jobs: selectedJobs, action })
  }

  function handleDetailAction(jobId: string, action: BulkAction) {
    const job = jobs.find((j) => j.id === jobId)
    if (!job) return
    setPendingAction({ jobs: [job], action })
  }

  function handleActionComplete() {
    setPendingAction(null)
    setSelected(new Set())
    // Refetch the jobs list and invalidate any open detail panel
    queryClient.invalidateQueries({ queryKey: ['jobs', 'list'] })
    if (detailJobId) {
      queryClient.invalidateQueries({ queryKey: ['job', detailJobId] })
    }
  }

  // ── Resize drag ────────────────────────────────────────────────────────────
  const resizingRef = useRef(false)

  function handleResizeStart(e: React.MouseEvent) {
    e.preventDefault()
    resizingRef.current = true
    const startX = e.clientX
    const startWidth = panelWidth

    function onMove(ev: MouseEvent) {
      // Dragging left (negative delta) widens the right panel
      const delta = startX - ev.clientX
      setPanelWidth(Math.max(PANEL_MIN, Math.min(PANEL_MAX, startWidth + delta)))
    }

    function onUp() {
      resizingRef.current = false
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }

    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
  }

  return (
    <div className={`flex flex-col h-screen bg-background ${selected.size > 0 ? 'pb-16' : ''}`}>
      {/* Header */}
      <header className="border-b px-6 py-4 flex items-center justify-between shrink-0">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Jobs</h1>
          <p className="text-sm text-muted-foreground">
            Monitor and manage the document processing queue
          </p>
        </div>
        <div className="text-sm text-muted-foreground">
          {!isLoading && (
            <span>
              {jobs.length} job{jobs.length !== 1 ? 's' : ''}
            </span>
          )}
        </div>
      </header>

      {/* Filter bar */}
      <JobFilterBar filters={filters} onChange={setFilters} />

      {/* Main content: table + resize handle + detail panel */}
      <div className="flex flex-1 overflow-hidden">
        {/* Table — fills remaining width */}
        <div className="flex-1 overflow-hidden min-w-0">
          <JobsTable
            jobs={jobs}
            isLoading={isLoading}
            selected={selected}
            onSelectAll={handleSelectAll}
            onSelectOne={handleSelectOne}
            onRowClick={handleRowClick}
            activeJobId={detailJobId}
          />
        </div>

        {/* Resize handle + detail panel */}
        {detailJobId !== null && (
          <>
            {/* Drag handle: 4px wide, 8px invisible hit zone */}
            <div
              className="group relative w-1 shrink-0 cursor-col-resize select-none"
              onMouseDown={handleResizeStart}
            >
              <div className="absolute inset-y-0 -left-1.5 -right-1.5" />
              <div className="h-full w-px bg-border group-hover:bg-primary/40 transition-colors" />
            </div>

            {/* Detail panel — fixed width, set by drag */}
            <div
              style={{ width: panelWidth }}
              className="shrink-0 border-l bg-background overflow-hidden"
            >
              <JobDetailPanel
                jobId={detailJobId}
                onClose={() => setDetailJobId(null)}
                onAction={handleDetailAction}
              />
            </div>
          </>
        )}
      </div>

      {/* Bulk action bar — fixed bottom, appears when rows selected */}
      {selected.size > 0 && (
        <BulkActionBar
          count={selected.size}
          onHold={() => handleBulkTrigger('hold')}
          onRelease={() => handleBulkTrigger('release')}
          onDelete={() => handleBulkTrigger('delete')}
        />
      )}

      {/* Sequential bulk confirmation dialog */}
      {pendingAction !== null && (
        <BulkConfirmDialog
          jobs={pendingAction.jobs}
          action={pendingAction.action}
          onComplete={handleActionComplete}
          onCancel={() => setPendingAction(null)}
        />
      )}
    </div>
  )
}
