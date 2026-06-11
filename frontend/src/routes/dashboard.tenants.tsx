import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { tenantsApi, type Tenant } from '@/api/client'
import { useAuthStore } from '@/stores/authStore'
import { useState } from 'react'

export const Route = createFileRoute('/dashboard/tenants')({
  component: TenantsPage,
})

function TenantsPage() {
  const { isAdmin } = useAuthStore()
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ name: '', slug: '' })
  const [error, setError] = useState<string | null>(null)

  const { data: tenants, isLoading } = useQuery({
    queryKey: ['tenants'],
    queryFn: tenantsApi.list,
  })

  const createMutation = useMutation({
    mutationFn: () => tenantsApi.create(form.name, form.slug),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tenants'] })
      setCreating(false)
      setForm({ name: '', slug: '' })
      setError(null)
    },
    onError: (e: Error) => setError(e.message),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => tenantsApi.delete(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tenants'] }),
  })

  const autoSlug = (name: string) =>
    name.toLowerCase().replace(/\s+/g, '-').replace(/[^a-z0-9-]/g, '')

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold">Tenants</h1>
          <p className="text-sm text-muted-foreground">Manage tenant organizations</p>
        </div>
        {isAdmin && !creating && (
          <button
            onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90"
          >
            New tenant
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New tenant</p>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Name</label>
              <input
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.name}
                onChange={(e) => {
                  setForm({ name: e.target.value, slug: autoSlug(e.target.value) })
                }}
                placeholder="Acme Corp"
              />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Slug</label>
              <input
                className="w-full border rounded px-2 py-1.5 text-sm bg-background font-mono"
                value={form.slug}
                onChange={(e) => setForm((f) => ({ ...f, slug: e.target.value }))}
                placeholder="acme-corp"
              />
            </div>
          </div>
          {error && <p className="text-xs text-destructive">{error}</p>}
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!form.name || !form.slug || createMutation.isPending}
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

      {tenants && tenants.length === 0 && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No tenants yet.</p>
        </div>
      )}

      {tenants && tenants.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Slug</th>
                <th className="text-left px-4 py-2 font-medium">Created</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {tenants.map((t: Tenant) => (
                <tr key={t.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2">
                    <Link
                      to="/dashboard/tenants/$tenantId"
                      params={{ tenantId: t.id }}
                      className="font-medium hover:underline"
                    >
                      {t.name}
                    </Link>
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">{t.slug}</td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {new Date(t.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-2 text-right">
                    {isAdmin && (
                      <button
                        onClick={() => {
                          if (confirm(`Delete tenant "${t.name}"?`)) {
                            deleteMutation.mutate(t.id)
                          }
                        }}
                        className="text-xs text-destructive hover:underline"
                      >
                        Delete
                      </button>
                    )}
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
