import { useQuery } from '@tanstack/react-query'
import { fetchJobs } from '@/api/jobs'
import type { JobFilters } from '@/types/job'

export function useJobs(filters: JobFilters) {
  const { data: jobs = [], isLoading, error } = useQuery({
    queryKey: ['jobs', 'list', filters],
    queryFn: () => fetchJobs({
      tenant_id: filters.tenant_id || undefined,
      system_id: filters.system_id || undefined,
      status: filters.statuses.length > 0 ? filters.statuses : undefined,
      date_from: filters.date_from || undefined,
      date_to: filters.date_to || undefined,
      search: filters.search || undefined,
    }),
    staleTime: 30_000,
  })

  return { jobs, isLoading, error }
}
