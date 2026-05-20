import type { Tenant, SystemRef } from '@/types/job'

export const MOCK_TENANTS: Tenant[] = [
  { id: '00000000-0000-0000-0000-000000000001', name: 'Dev Tenant', slug: 'dev' },
  { id: 'aaaaaaaa-0000-0000-0000-000000000001', name: 'Bank Hapoalim', slug: 'hapoalim' },
]

export const MOCK_SYSTEMS: SystemRef[] = [
  {
    id: '00000000-0000-0000-0000-000000000002',
    tenant_id: '00000000-0000-0000-0000-000000000001',
    name: 'Default',
  },
  {
    id: 'bbbbbbbb-0000-0000-0000-000000000002',
    tenant_id: 'aaaaaaaa-0000-0000-0000-000000000001',
    name: 'HapoalimClassification',
  },
  {
    id: 'cccccccc-0000-0000-0000-000000000002',
    tenant_id: 'aaaaaaaa-0000-0000-0000-000000000001',
    name: 'HapoalimExport',
  },
]
