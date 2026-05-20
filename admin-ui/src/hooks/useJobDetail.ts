import { useQuery } from '@tanstack/react-query'
import {
  fetchJobDetail,
  fetchJobTransitions,
  fetchFieldsSummary,
  fetchJobArtifacts,
} from '@/api/jobs'
import type { JobDetail, JobTransition, FieldsSummary, ArtifactWithUrl } from '@/types/job'

export function useJobDetail(id: string) {
  return useQuery<JobDetail>({
    queryKey: ['job', id],
    queryFn: () => fetchJobDetail(id),
    staleTime: 30_000,
  })
}

export function useJobTransitions(id: string) {
  return useQuery<JobTransition[]>({
    queryKey: ['job', id, 'transitions'],
    queryFn: () => fetchJobTransitions(id),
    staleTime: 30_000,
  })
}

export function useJobArtifacts(id: string, enabled: boolean) {
  return useQuery<ArtifactWithUrl[]>({
    queryKey: ['job', id, 'artifacts'],
    queryFn: () => fetchJobArtifacts(id),
    enabled,
    staleTime: 14 * 60 * 1000, // just under the 15-min presigned URL TTL
  })
}

export function useJobFieldsSummary(id: string, enabled: boolean) {
  return useQuery<FieldsSummary>({
    queryKey: ['job', id, 'fields-summary'],
    queryFn: () => fetchFieldsSummary(id),
    enabled,
    staleTime: 60_000,
  })
}
