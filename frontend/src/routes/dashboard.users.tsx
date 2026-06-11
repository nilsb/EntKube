import { createFileRoute } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { usersApi, type User } from '@/api/client'
import { useState } from 'react'

export const Route = createFileRoute('/dashboard/users')({
  component: UsersPage,
})

function UsersPage() {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ email: '', password: '', is_admin: false })
  const [error, setError] = useState<string | null>(null)

  const { data: users, isLoading } = useQuery({
    queryKey: ['users'],
    queryFn: usersApi.list,
  })

  const createMutation = useMutation({
    mutationFn: () => usersApi.create(form.email, form.password, form.is_admin),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] })
      setCreating(false)
      setForm({ email: '', password: '', is_admin: false })
      setError(null)
    },
    onError: (e: Error) => setError(e.message),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => usersApi.delete(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })

  const toggleAdminMutation = useMutation({
    mutationFn: ({ id, is_admin }: { id: string; is_admin: boolean }) =>
      usersApi.update(id, { is_admin }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">Users</h1>
          <p className="text-sm text-muted-foreground">Manage user accounts</p>
        </div>
        {!creating && (
          <button
            onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90"
          >
            New user
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New user</p>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Email</label>
              <input
                type="email"
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.email}
                onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
                placeholder="user@example.com"
              />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Password</label>
              <input
                type="password"
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.password}
                onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
                placeholder="••••••••"
              />
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm cursor-pointer">
            <input
              type="checkbox"
              checked={form.is_admin}
              onChange={(e) => setForm((f) => ({ ...f, is_admin: e.target.checked }))}
            />
            Administrator
          </label>
          {error && <p className="text-xs text-destructive">{error}</p>}
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!form.email || !form.password || createMutation.isPending}
              className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90 disabled:opacity-50"
            >
              {createMutation.isPending ? 'Creating…' : 'Create'}
            </button>
            <button
              onClick={() => { setCreating(false); setError(null) }}
              className="px-3 py-1.5 text-sm border rounded-md hover:bg-accent"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}

      {users && users.length === 0 && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No users.</p>
        </div>
      )}

      {users && users.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Email</th>
                <th className="text-left px-4 py-2 font-medium">Role</th>
                <th className="text-left px-4 py-2 font-medium">Created</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {users.map((u: User) => (
                <tr key={u.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2">{u.email}</td>
                  <td className="px-4 py-2">
                    {u.is_admin ? (
                      <span className="inline-flex px-1.5 py-0.5 rounded text-xs font-medium bg-primary/10 text-primary">
                        Admin
                      </span>
                    ) : (
                      <span className="text-muted-foreground text-xs">Member</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {new Date(u.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <div className="flex gap-3 justify-end">
                      <button
                        onClick={() =>
                          toggleAdminMutation.mutate({ id: u.id, is_admin: !u.is_admin })
                        }
                        className="text-xs text-muted-foreground hover:underline"
                      >
                        {u.is_admin ? 'Remove admin' : 'Make admin'}
                      </button>
                      <button
                        onClick={() => {
                          if (confirm(`Delete user "${u.email}"?`)) {
                            deleteMutation.mutate(u.id)
                          }
                        }}
                        className="text-xs text-destructive hover:underline"
                      >
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
