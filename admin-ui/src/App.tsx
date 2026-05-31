import { useQuery, useQueryClient } from '@tanstack/react-query'
import { me, logout } from '@/api/auth'
import { LoginPage } from '@/pages/auth/LoginPage'
import { JobsAdminPage } from '@/pages/jobs/JobsAdminPage'

export default function App() {
  const qc = useQueryClient()

  const { isLoading, isError } = useQuery({
    queryKey: ['auth', 'me'],
    queryFn: me,
    retry: false,
    staleTime: 5 * 60 * 1000,
  })

  async function handleLogout() {
    await logout()
    qc.invalidateQueries({ queryKey: ['auth'] })
  }

  if (isLoading) {
    return (
      <div className="flex h-screen items-center justify-center text-sm text-gray-400">
        Loading…
      </div>
    )
  }

  if (isError) {
    return (
      <LoginPage onSuccess={() => qc.invalidateQueries({ queryKey: ['auth'] })} />
    )
  }

  return (
    <div className="relative">
      <button
        onClick={handleLogout}
        className="absolute right-4 top-3 z-50 rounded-md px-3 py-1.5 text-xs font-medium text-gray-500 hover:bg-gray-100 hover:text-gray-700"
      >
        Sign out
      </button>
      <JobsAdminPage />
    </div>
  )
}
