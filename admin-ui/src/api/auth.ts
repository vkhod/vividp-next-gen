export async function me(): Promise<{ ok: boolean }> {
  const res = await fetch('/api/auth/me')
  if (!res.ok) throw new Error('not authenticated')
  return res.json()
}

export async function login(username: string, password: string): Promise<void> {
  const res = await fetch('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  if (res.status === 401) throw new Error('Invalid credentials')
  if (!res.ok) throw new Error('Login failed')
}

export async function logout(): Promise<void> {
  await fetch('/api/auth/logout', { method: 'POST' })
}
