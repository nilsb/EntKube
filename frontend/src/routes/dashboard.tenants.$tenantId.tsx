import { createFileRoute } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  tenantsApi, clustersApi, deploymentsApi, vaultApi, alertsApi,
  customersApi, gitReposApi, notifApi, environmentsApi, componentsApi,
  maintenanceApi, alertRulesApi,
  type Cluster, type Deployment, type Secret, type AlertIncident, type Customer,
  type GitRepo, type NotificationChannel, type Environment, type ClusterComponent,
  type MaintenanceWindow, type AlertRule,
} from '@/api/client'
import { useState } from 'react'

export const Route = createFileRoute('/dashboard/tenants/$tenantId')({
  component: TenantDetailPage,
})

type Tab = 'clusters' | 'deployments' | 'alerts' | 'secrets' | 'customers' | 'git-repos' | 'notifications' | 'environments' | 'maintenance' | 'alert-rules'

function StatusDot({ status }: { status: string }) {
  const color =
    status === 'healthy' || status === 'synced' ? 'bg-green-500' :
    status === 'degraded' || status === 'failed' || status === 'out_of_sync' ? 'bg-red-500' :
    status === 'progressing' || status === 'syncing' ? 'bg-yellow-500' :
    'bg-gray-400'
  return <span className={`inline-block w-2 h-2 rounded-full ${color}`} />
}

function SeverityBadge({ severity }: { severity: string }) {
  const cls =
    severity === 'critical' ? 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200' :
    severity === 'warning' ? 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200' :
    'bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-200'
  return (
    <span className={`inline-flex px-1.5 py-0.5 rounded text-xs font-medium ${cls}`}>
      {severity}
    </span>
  )
}

function TenantDetailPage() {
  const { tenantId } = Route.useParams()
  const [tab, setTab] = useState<Tab>('clusters')

  const { data: tenant } = useQuery({
    queryKey: ['tenants', tenantId],
    queryFn: () => tenantsApi.get(tenantId),
  })

  const tabs: { id: Tab; label: string }[] = [
    { id: 'clusters',      label: 'Clusters' },
    { id: 'deployments',   label: 'Deployments' },
    { id: 'alerts',        label: 'Alerts' },
    { id: 'secrets',       label: 'Secrets' },
    { id: 'customers',     label: 'Customers' },
    { id: 'git-repos',     label: 'Git repos' },
    { id: 'notifications', label: 'Notifications' },
    { id: 'environments',  label: 'Environments' },
    { id: 'maintenance',   label: 'Maintenance' },
    { id: 'alert-rules',   label: 'Alert rules' },
  ]

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold">{tenant?.name ?? '…'}</h1>
        {tenant && <p className="text-xs text-muted-foreground font-mono">{tenant.slug}</p>}
      </div>

      {/* Tab bar */}
      <div className="flex gap-1 border-b">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`px-4 py-2 text-sm -mb-px border-b-2 transition-colors ${
              tab === t.id
                ? 'border-primary text-foreground font-medium'
                : 'border-transparent text-muted-foreground hover:text-foreground'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'clusters'      && <ClustersTab tenantId={tenantId} />}
      {tab === 'deployments'   && <DeploymentsTab tenantId={tenantId} />}
      {tab === 'alerts'        && <AlertsTab tenantId={tenantId} />}
      {tab === 'secrets'       && <SecretsTab tenantId={tenantId} />}
      {tab === 'customers'     && <CustomersTab tenantId={tenantId} />}
      {tab === 'git-repos'     && <GitReposTab tenantId={tenantId} />}
      {tab === 'notifications' && <NotificationsTab tenantId={tenantId} />}
      {tab === 'environments'  && <EnvironmentsTab tenantId={tenantId} />}
      {tab === 'maintenance'   && <MaintenanceTab tenantId={tenantId} />}
      {tab === 'alert-rules'   && <AlertRulesTab tenantId={tenantId} />}
    </div>
  )
}

// ────────────────────────────────────────────────────────────────
// Clusters tab
// ────────────────────────────────────────────────────────────────

function ClustersTab({ tenantId }: { tenantId: string }) {
  const [expanded, setExpanded] = useState<string | null>(null)
  const { data: clusters, isLoading } = useQuery({
    queryKey: ['clusters', tenantId],
    queryFn: () => clustersApi.list(tenantId),
  })

  return (
    <div className="space-y-3">
      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {clusters?.length === 0 && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No clusters configured.</p>
        </div>
      )}
      {clusters && clusters.length > 0 && (
        <div className="border rounded-lg overflow-hidden divide-y">
          {clusters.map((c: Cluster) => (
            <div key={c.id}>
              <div
                className="flex items-center gap-3 px-4 py-3 hover:bg-muted/25 cursor-pointer"
                onClick={() => setExpanded(expanded === c.id ? null : c.id)}
              >
                <span className="text-muted-foreground text-xs">{expanded === c.id ? '▼' : '▶'}</span>
                <div className="flex-1 min-w-0">
                  <p className="font-medium text-sm">{c.name}</p>
                  <p className="text-xs text-muted-foreground font-mono truncate">{c.api_server_url}</p>
                </div>
                <span className="text-xs text-muted-foreground">{new Date(c.created_at).toLocaleDateString()}</span>
              </div>
              {expanded === c.id && (
                <div className="bg-muted/20 px-6 pb-4 pt-2">
                  <ClusterComponentsPanel tenantId={tenantId} clusterId={c.id} clusterName={c.name} />
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function ClusterComponentsPanel({
  tenantId, clusterId, clusterName,
}: { tenantId: string; clusterId: string; clusterName: string }) {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({
    name: '', helm_chart_name: '', helm_repo_url: '', helm_chart_version: '',
    release_name: '', namespace: '',
  })

  const { data: comps, isLoading } = useQuery({
    queryKey: ['components', clusterId],
    queryFn: () => componentsApi.list(tenantId, clusterId),
  })

  const createMutation = useMutation({
    mutationFn: () => componentsApi.create(tenantId, clusterId, {
      name: form.name,
      helm_chart_name: form.helm_chart_name || undefined,
      helm_repo_url: form.helm_repo_url || undefined,
      helm_chart_version: form.helm_chart_version || undefined,
      release_name: form.release_name || undefined,
      namespace: form.namespace || undefined,
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['components', clusterId] })
      setCreating(false)
      setForm({ name: '', helm_chart_name: '', helm_repo_url: '', helm_chart_version: '', release_name: '', namespace: '' })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => componentsApi.delete(tenantId, clusterId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['components', clusterId] }),
  })

  const statusColor = (s: string) =>
    s === 'installed' ? 'text-green-600 dark:text-green-400' :
    s === 'failed' ? 'text-red-600 dark:text-red-400' :
    s === 'installing' ? 'text-yellow-600 dark:text-yellow-400' :
    'text-muted-foreground'

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          Components — {clusterName}
        </p>
        {!creating && (
          <button
            onClick={() => setCreating(true)}
            className="text-xs px-2 py-1 bg-primary text-primary-foreground rounded hover:opacity-90"
          >
            Add component
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded p-3 bg-card space-y-2 text-sm">
          <div className="grid grid-cols-2 gap-2">
            {[
              ['name', 'Component name *'],
              ['helm_chart_name', 'Helm chart'],
              ['helm_repo_url', 'Helm repo URL'],
              ['helm_chart_version', 'Chart version'],
              ['release_name', 'Release name'],
              ['namespace', 'Namespace'],
            ].map(([key, label]) => (
              <div key={key}>
                <label className="text-xs text-muted-foreground">{label}</label>
                <input
                  className="w-full border rounded px-2 py-1 text-sm bg-background"
                  value={form[key as keyof typeof form]}
                  onChange={(e) => setForm((f) => ({ ...f, [key]: e.target.value }))}
                />
              </div>
            ))}
          </div>
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!form.name || createMutation.isPending}
              className="px-3 py-1 text-sm bg-primary text-primary-foreground rounded hover:opacity-90 disabled:opacity-50"
            >
              {createMutation.isPending ? 'Adding…' : 'Add'}
            </button>
            <button onClick={() => setCreating(false)} className="px-3 py-1 text-sm border rounded hover:bg-accent">
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-xs text-muted-foreground">Loading…</p>}
      {!isLoading && comps?.length === 0 && !creating && (
        <p className="text-xs text-muted-foreground">No components installed.</p>
      )}
      {comps && comps.length > 0 && (
        <table className="w-full text-xs">
          <thead>
            <tr className="text-muted-foreground">
              <th className="text-left py-1 font-medium">Name</th>
              <th className="text-left py-1 font-medium">Chart</th>
              <th className="text-left py-1 font-medium">Namespace</th>
              <th className="text-left py-1 font-medium">Status</th>
              <th className="py-1" />
            </tr>
          </thead>
          <tbody className="divide-y divide-muted/30">
            {comps.map((comp: ClusterComponent) => (
              <tr key={comp.id}>
                <td className="py-1.5 font-medium">{comp.name}</td>
                <td className="py-1.5 text-muted-foreground">
                  {comp.helm_chart_name ?? '—'}{comp.helm_chart_version ? `@${comp.helm_chart_version}` : ''}
                </td>
                <td className="py-1.5 font-mono text-muted-foreground">{comp.namespace ?? '—'}</td>
                <td className={`py-1.5 font-medium ${statusColor(comp.status)}`}>{comp.status}</td>
                <td className="py-1.5 text-right">
                  <button
                    onClick={() => {
                      if (confirm(`Remove component "${comp.name}"?`)) deleteMutation.mutate(comp.id)
                    }}
                    className="text-destructive hover:underline"
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

// ────────────────────────────────────────────────────────────────
// Deployments tab
// ────────────────────────────────────────────────────────────────

function DeploymentsTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const { data: deployments, isLoading } = useQuery({
    queryKey: ['deployments', tenantId],
    queryFn: () => deploymentsApi.list(tenantId),
    refetchInterval: 30_000,
  })

  const syncMutation = useMutation({
    mutationFn: (id: string) => deploymentsApi.triggerSync(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['deployments', tenantId] }),
  })

  return (
    <div className="space-y-3">
      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {deployments?.length === 0 && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No deployments.</p>
        </div>
      )}
      {deployments && deployments.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Type</th>
                <th className="text-left px-4 py-2 font-medium">Namespace</th>
                <th className="text-left px-4 py-2 font-medium">Sync</th>
                <th className="text-left px-4 py-2 font-medium">Health</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {deployments.map((d: Deployment) => (
                <tr key={d.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2 font-medium">{d.name}</td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">{d.deployment_type}</td>
                  <td className="px-4 py-2 font-mono text-xs">{d.namespace}</td>
                  <td className="px-4 py-2">
                    <span className="flex items-center gap-1.5">
                      <StatusDot status={d.sync_status} />
                      <span className="text-xs">{d.sync_status}</span>
                    </span>
                  </td>
                  <td className="px-4 py-2">
                    <span className="flex items-center gap-1.5">
                      <StatusDot status={d.health_status} />
                      <span className="text-xs">{d.health_status}</span>
                    </span>
                  </td>
                  <td className="px-4 py-2 text-right">
                    {(d.deployment_type.startsWith('git_') || d.deployment_type === 'yaml') && (
                      <button
                        onClick={() => syncMutation.mutate(d.id)}
                        disabled={syncMutation.isPending}
                        className="text-xs text-primary hover:underline disabled:opacity-50"
                      >
                        Sync
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

// ────────────────────────────────────────────────────────────────
// Alerts tab
// ────────────────────────────────────────────────────────────────

function AlertsTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const [statusFilter, setStatusFilter] = useState('active')

  const { data: alerts, isLoading } = useQuery({
    queryKey: ['alerts', tenantId, statusFilter],
    queryFn: () => alertsApi.list(tenantId, { status: statusFilter || undefined }),
    refetchInterval: 30_000,
  })

  const ackMutation = useMutation({
    mutationFn: (id: string) => alertsApi.acknowledge(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alerts', tenantId] }),
  })

  const resolveMutation = useMutation({
    mutationFn: (id: string) => alertsApi.resolve(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alerts', tenantId] }),
  })

  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        {['active', 'acknowledged', 'resolved', ''].map((s) => (
          <button
            key={s}
            onClick={() => setStatusFilter(s)}
            className={`px-3 py-1 text-xs rounded-full border transition-colors ${
              statusFilter === s
                ? 'bg-primary text-primary-foreground border-primary'
                : 'hover:bg-accent'
            }`}
          >
            {s || 'All'}
          </button>
        ))}
      </div>

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {alerts?.length === 0 && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No alert incidents.</p>
        </div>
      )}
      {alerts && alerts.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Alert</th>
                <th className="text-left px-4 py-2 font-medium">Cluster</th>
                <th className="text-left px-4 py-2 font-medium">Severity</th>
                <th className="text-left px-4 py-2 font-medium">Status</th>
                <th className="text-left px-4 py-2 font-medium">Started</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {alerts.map((a: AlertIncident) => (
                <tr key={a.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2">
                    <p className="font-medium">{a.alert_name}</p>
                    {a.summary && (
                      <p className="text-xs text-muted-foreground truncate max-w-xs">{a.summary}</p>
                    )}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">{a.cluster_name}</td>
                  <td className="px-4 py-2">
                    <SeverityBadge severity={a.severity} />
                  </td>
                  <td className="px-4 py-2 text-xs capitalize">{a.status}</td>
                  <td className="px-4 py-2 text-muted-foreground text-xs">
                    {new Date(a.starts_at).toLocaleString()}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <div className="flex gap-2 justify-end">
                      {a.status === 'active' && (
                        <button
                          onClick={() => ackMutation.mutate(a.id)}
                          className="text-xs text-primary hover:underline"
                        >
                          Ack
                        </button>
                      )}
                      {a.status !== 'resolved' && (
                        <button
                          onClick={() => resolveMutation.mutate(a.id)}
                          className="text-xs text-muted-foreground hover:underline"
                        >
                          Resolve
                        </button>
                      )}
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

// ────────────────────────────────────────────────────────────────
// Secrets tab
// ────────────────────────────────────────────────────────────────

function SecretsTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ key: '', value: '' })
  const [revealed, setRevealed] = useState<Record<string, boolean>>({})

  const { data: secrets, isLoading } = useQuery({
    queryKey: ['secrets', tenantId],
    queryFn: () => vaultApi.list(tenantId),
  })

  const upsertMutation = useMutation({
    mutationFn: () => vaultApi.upsert(tenantId, form.key, form.value),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['secrets', tenantId] })
      setCreating(false)
      setForm({ key: '', value: '' })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (key: string) => vaultApi.delete(tenantId, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['secrets', tenantId] }),
  })

  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        {!creating && (
          <button
            onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90"
          >
            Add secret
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New secret</p>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Key name</label>
              <input
                className="w-full border rounded px-2 py-1.5 text-sm bg-background font-mono"
                value={form.key}
                onChange={(e) => setForm((f) => ({ ...f, key: e.target.value }))}
                placeholder="my-secret"
              />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Value</label>
              <input
                type="password"
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.value}
                onChange={(e) => setForm((f) => ({ ...f, value: e.target.value }))}
                placeholder="••••••••"
              />
            </div>
          </div>
          <div className="flex gap-2">
            <button
              onClick={() => upsertMutation.mutate()}
              disabled={!form.key || !form.value || upsertMutation.isPending}
              className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90 disabled:opacity-50"
            >
              {upsertMutation.isPending ? 'Saving…' : 'Save'}
            </button>
            <button
              onClick={() => setCreating(false)}
              className="px-3 py-1.5 text-sm border rounded-md hover:bg-accent"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {secrets?.length === 0 && !creating && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No secrets stored.</p>
        </div>
      )}
      {secrets && secrets.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Key</th>
                <th className="text-left px-4 py-2 font-medium">Value</th>
                <th className="text-left px-4 py-2 font-medium">Updated</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {secrets.map((s: Secret) => (
                <tr key={s.key_name} className="hover:bg-muted/25">
                  <td className="px-4 py-2 font-mono text-xs">{s.key_name}</td>
                  <td className="px-4 py-2 font-mono text-xs">
                    {revealed[s.key_name] ? s.value : '••••••••'}
                    <button
                      onClick={() => setRevealed((r) => ({ ...r, [s.key_name]: !r[s.key_name] }))}
                      className="ml-2 text-xs text-muted-foreground hover:underline"
                    >
                      {revealed[s.key_name] ? 'hide' : 'show'}
                    </button>
                  </td>
                  <td className="px-4 py-2 text-muted-foreground text-xs">
                    {new Date(s.updated_at).toLocaleString()}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      onClick={() => {
                        if (confirm(`Delete secret "${s.key_name}"?`)) {
                          deleteMutation.mutate(s.key_name)
                        }
                      }}
                      className="text-xs text-destructive hover:underline"
                    >
                      Delete
                    </button>
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

// ────────────────────────────────────────────────────────────────
// Git repos tab
// ────────────────────────────────────────────────────────────────

function GitReposTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({
    name: '', url: '', auth_type: 'none', username: '',
    credential: '', default_branch: 'main',
  })

  const { data: repos, isLoading } = useQuery({
    queryKey: ['git-repos', tenantId],
    queryFn: () => gitReposApi.list(tenantId),
  })

  const createMutation = useMutation({
    mutationFn: () => gitReposApi.create(tenantId, form),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['git-repos', tenantId] })
      setCreating(false)
      setForm({ name: '', url: '', auth_type: 'none', username: '', credential: '', default_branch: 'main' })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => gitReposApi.delete(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['git-repos', tenantId] }),
  })

  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        {!creating && (
          <button onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90">
            Add repository
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New git repository</p>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Name</label>
              <input className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.name} onChange={(e) => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="my-repo" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">URL</label>
              <input className="w-full border rounded px-2 py-1.5 text-sm bg-background font-mono"
                value={form.url} onChange={(e) => setForm(f => ({ ...f, url: e.target.value }))}
                placeholder="https://github.com/org/repo" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Auth type</label>
              <select className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.auth_type} onChange={(e) => setForm(f => ({ ...f, auth_type: e.target.value }))}>
                <option value="none">None (public)</option>
                <option value="https_pat">HTTPS token / PAT</option>
                <option value="https_password">HTTPS password</option>
              </select>
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Default branch</label>
              <input className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.default_branch} onChange={(e) => setForm(f => ({ ...f, default_branch: e.target.value }))}
                placeholder="main" />
            </div>
            {form.auth_type !== 'none' && (
              <>
                <div className="space-y-1">
                  <label className="text-xs text-muted-foreground">Username</label>
                  <input className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                    value={form.username} onChange={(e) => setForm(f => ({ ...f, username: e.target.value }))} />
                </div>
                <div className="space-y-1">
                  <label className="text-xs text-muted-foreground">Token / Password</label>
                  <input type="password" className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                    value={form.credential} onChange={(e) => setForm(f => ({ ...f, credential: e.target.value }))} />
                </div>
              </>
            )}
          </div>
          <div className="flex gap-2">
            <button onClick={() => createMutation.mutate()}
              disabled={!form.name || !form.url || createMutation.isPending}
              className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90 disabled:opacity-50">
              {createMutation.isPending ? 'Adding…' : 'Add'}
            </button>
            <button onClick={() => setCreating(false)}
              className="px-3 py-1.5 text-sm border rounded-md hover:bg-accent">Cancel</button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {repos?.length === 0 && !creating && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No git repositories configured.</p>
        </div>
      )}
      {repos && repos.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">URL</th>
                <th className="text-left px-4 py-2 font-medium">Auth</th>
                <th className="text-left px-4 py-2 font-medium">Branch</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {repos.map((r: GitRepo) => (
                <tr key={r.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2 font-medium">{r.name}</td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground truncate max-w-xs">{r.url}</td>
                  <td className="px-4 py-2 text-xs capitalize">{r.auth_type.replace(/_/g, ' ')}</td>
                  <td className="px-4 py-2 font-mono text-xs">{r.default_branch}</td>
                  <td className="px-4 py-2 text-right">
                    <button onClick={() => { if (confirm(`Delete "${r.name}"?`)) deleteMutation.mutate(r.id) }}
                      className="text-xs text-destructive hover:underline">Delete</button>
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

// ────────────────────────────────────────────────────────────────
// Notifications tab
// ────────────────────────────────────────────────────────────────

function NotificationsTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({
    name: '', channel_type: 'slack', url: '', severity_filter: 'all',
  })
  const [testResult, setTestResult] = useState<Record<string, string>>({})

  const { data: channels, isLoading } = useQuery({
    queryKey: ['notification-channels', tenantId],
    queryFn: () => notifApi.list(tenantId),
  })

  const createMutation = useMutation({
    mutationFn: () => notifApi.create(tenantId, {
      name: form.name,
      channel_type: form.channel_type,
      config: { url: form.url },
      severity_filter: form.severity_filter,
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['notification-channels', tenantId] })
      setCreating(false)
      setForm({ name: '', channel_type: 'slack', url: '', severity_filter: 'all' })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => notifApi.delete(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notification-channels', tenantId] }),
  })

  const testMutation = useMutation({
    mutationFn: (id: string) => notifApi.test(tenantId, id),
    onSuccess: (data, id) => setTestResult(r => ({ ...r, [id]: data.status === 'ok' ? '✓ sent' : `✗ ${data.message}` })),
    onError: (_err, id) => setTestResult(r => ({ ...r, [id]: '✗ error' })),
  })

  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        {!creating && (
          <button onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90">
            Add channel
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New notification channel</p>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Name</label>
              <input className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.name} onChange={(e) => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="Slack #alerts" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Type</label>
              <select className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.channel_type} onChange={(e) => setForm(f => ({ ...f, channel_type: e.target.value }))}>
                <option value="slack">Slack</option>
                <option value="teams">Microsoft Teams</option>
                <option value="webhook">Generic webhook</option>
              </select>
            </div>
            <div className="space-y-1 col-span-2">
              <label className="text-xs text-muted-foreground">Webhook URL</label>
              <input className="w-full border rounded px-2 py-1.5 text-sm bg-background font-mono"
                value={form.url} onChange={(e) => setForm(f => ({ ...f, url: e.target.value }))}
                placeholder="https://hooks.slack.com/services/…" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">Severity filter</label>
              <select className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.severity_filter} onChange={(e) => setForm(f => ({ ...f, severity_filter: e.target.value }))}>
                <option value="all">All alerts</option>
                <option value="warning_and_above">Warning and above</option>
                <option value="critical_only">Critical only</option>
              </select>
            </div>
          </div>
          <div className="flex gap-2">
            <button onClick={() => createMutation.mutate()}
              disabled={!form.name || !form.url || createMutation.isPending}
              className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90 disabled:opacity-50">
              {createMutation.isPending ? 'Adding…' : 'Add'}
            </button>
            <button onClick={() => setCreating(false)}
              className="px-3 py-1.5 text-sm border rounded-md hover:bg-accent">Cancel</button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {channels?.length === 0 && !creating && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No notification channels configured.</p>
        </div>
      )}
      {channels && channels.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Type</th>
                <th className="text-left px-4 py-2 font-medium">Filter</th>
                <th className="text-left px-4 py-2 font-medium">Enabled</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {channels.map((c: NotificationChannel) => (
                <tr key={c.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2 font-medium">{c.name}</td>
                  <td className="px-4 py-2 text-xs capitalize">{c.channel_type}</td>
                  <td className="px-4 py-2 text-xs">{c.severity_filter.replace(/_/g, ' ')}</td>
                  <td className="px-4 py-2">
                    <span className={`text-xs ${c.is_enabled ? 'text-green-600' : 'text-muted-foreground'}`}>
                      {c.is_enabled ? 'Yes' : 'No'}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-right">
                    <div className="flex gap-3 justify-end items-center">
                      {testResult[c.id] && (
                        <span className="text-xs text-muted-foreground">{testResult[c.id]}</span>
                      )}
                      <button onClick={() => testMutation.mutate(c.id)}
                        disabled={testMutation.isPending}
                        className="text-xs text-primary hover:underline disabled:opacity-50">Test</button>
                      <button onClick={() => { if (confirm(`Delete "${c.name}"?`)) deleteMutation.mutate(c.id) }}
                        className="text-xs text-destructive hover:underline">Delete</button>
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

// ────────────────────────────────────────────────────────────────
// ────────────────────────────────────────────────────────────────
// Environments tab
// ────────────────────────────────────────────────────────────────

function EnvironmentsTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')

  const { data: envs, isLoading } = useQuery({
    queryKey: ['environments', tenantId],
    queryFn: () => environmentsApi.list(tenantId),
  })

  const createMutation = useMutation({
    mutationFn: () => environmentsApi.create(tenantId, name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['environments', tenantId] })
      setCreating(false)
      setName('')
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => environmentsApi.delete(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['environments', tenantId] }),
  })

  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        {!creating && (
          <button
            onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90"
          >
            Add environment
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New environment</p>
          <input
            className="w-full border rounded px-2 py-1.5 text-sm bg-background"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. production, staging"
          />
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!name || createMutation.isPending}
              className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90 disabled:opacity-50"
            >
              {createMutation.isPending ? 'Creating…' : 'Create'}
            </button>
            <button onClick={() => setCreating(false)} className="px-3 py-1.5 text-sm border rounded-md hover:bg-accent">
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {envs?.length === 0 && !creating && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No environments yet.</p>
        </div>
      )}
      {envs && envs.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Created</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {envs.map((e: Environment) => (
                <tr key={e.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2 font-medium">{e.name}</td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {new Date(e.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      onClick={() => {
                        if (confirm(`Delete environment "${e.name}"?`)) deleteMutation.mutate(e.id)
                      }}
                      className="text-xs text-destructive hover:underline"
                    >
                      Delete
                    </button>
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

// ────────────────────────────────────────────────────────────────
// Maintenance windows tab
// ────────────────────────────────────────────────────────────────

function MaintenanceTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ name: '', starts_at: '', ends_at: '' })

  const { data: windows, isLoading } = useQuery({
    queryKey: ['maintenance', tenantId],
    queryFn: () => maintenanceApi.list(tenantId),
  })

  const createMutation = useMutation({
    mutationFn: () => maintenanceApi.create(tenantId, {
      name: form.name,
      starts_at: new Date(form.starts_at).toISOString(),
      ends_at: new Date(form.ends_at).toISOString(),
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['maintenance', tenantId] })
      setCreating(false)
      setForm({ name: '', starts_at: '', ends_at: '' })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => maintenanceApi.delete(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['maintenance', tenantId] }),
  })

  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        {!creating && (
          <button
            onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90"
          >
            Schedule window
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New maintenance window</p>
          <input
            className="w-full border rounded px-2 py-1.5 text-sm bg-background"
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            placeholder="Window name"
          />
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="text-xs text-muted-foreground">Starts at</label>
              <input
                type="datetime-local"
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.starts_at}
                onChange={(e) => setForm((f) => ({ ...f, starts_at: e.target.value }))}
              />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Ends at</label>
              <input
                type="datetime-local"
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.ends_at}
                onChange={(e) => setForm((f) => ({ ...f, ends_at: e.target.value }))}
              />
            </div>
          </div>
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!form.name || !form.starts_at || !form.ends_at || createMutation.isPending}
              className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90 disabled:opacity-50"
            >
              {createMutation.isPending ? 'Saving…' : 'Save'}
            </button>
            <button onClick={() => setCreating(false)} className="px-3 py-1.5 text-sm border rounded-md hover:bg-accent">
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {windows?.length === 0 && !creating && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No maintenance windows scheduled.</p>
        </div>
      )}
      {windows && windows.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Starts</th>
                <th className="text-left px-4 py-2 font-medium">Ends</th>
                <th className="text-left px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {windows.map((w: MaintenanceWindow) => (
                <tr key={w.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2 font-medium">{w.name}</td>
                  <td className="px-4 py-2 text-muted-foreground font-mono text-xs">
                    {new Date(w.starts_at).toLocaleString()}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground font-mono text-xs">
                    {new Date(w.ends_at).toLocaleString()}
                  </td>
                  <td className="px-4 py-2">
                    {w.is_active ? (
                      <span className="inline-flex items-center gap-1 text-xs text-green-700 dark:text-green-400">
                        <span className="w-1.5 h-1.5 rounded-full bg-green-500" />
                        Active
                      </span>
                    ) : new Date(w.ends_at) < new Date() ? (
                      <span className="text-xs text-muted-foreground">Completed</span>
                    ) : (
                      <span className="text-xs text-muted-foreground">Scheduled</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      onClick={() => {
                        if (confirm(`Delete maintenance window "${w.name}"?`)) deleteMutation.mutate(w.id)
                      }}
                      className="text-xs text-destructive hover:underline"
                    >
                      Delete
                    </button>
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

// ────────────────────────────────────────────────────────────────
// Alert routing rules tab
// ────────────────────────────────────────────────────────────────

function AlertRulesTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({
    name: '', priority: '100',
    suppress_incident: false,
    match_alert_name: '', match_namespace: '', match_severity: '',
  })

  const { data: rules, isLoading } = useQuery({
    queryKey: ['alert-rules', tenantId],
    queryFn: () => alertRulesApi.list(tenantId),
  })

  const createMutation = useMutation({
    mutationFn: () => alertRulesApi.create(tenantId, {
      name: form.name,
      priority: parseInt(form.priority) || 100,
      suppress_incident: form.suppress_incident,
      match_alert_name: form.match_alert_name || undefined,
      match_namespace: form.match_namespace || undefined,
      match_severity: form.match_severity || undefined,
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['alert-rules', tenantId] })
      setCreating(false)
      setForm({ name: '', priority: '100', suppress_incident: false, match_alert_name: '', match_namespace: '', match_severity: '' })
    },
  })

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      alertRulesApi.toggleEnabled(tenantId, id, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules', tenantId] }),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => alertRulesApi.delete(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules', tenantId] }),
  })

  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        {!creating && (
          <button
            onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90"
          >
            Add rule
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New alert routing rule</p>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="text-xs text-muted-foreground">Name</label>
              <input
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="Rule name"
              />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Priority (lower = first)</label>
              <input
                type="number"
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.priority}
                onChange={(e) => setForm((f) => ({ ...f, priority: e.target.value }))}
              />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Match alert name (optional)</label>
              <input
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.match_alert_name}
                onChange={(e) => setForm((f) => ({ ...f, match_alert_name: e.target.value }))}
                placeholder="e.g. KubePodCrashLooping"
              />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Match severity (optional)</label>
              <select
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.match_severity}
                onChange={(e) => setForm((f) => ({ ...f, match_severity: e.target.value }))}
              >
                <option value="">Any</option>
                <option value="critical">critical</option>
                <option value="warning">warning</option>
                <option value="info">info</option>
              </select>
            </div>
            <div>
              <label className="text-xs text-muted-foreground">Match namespace (optional)</label>
              <input
                className="w-full border rounded px-2 py-1.5 text-sm bg-background"
                value={form.match_namespace}
                onChange={(e) => setForm((f) => ({ ...f, match_namespace: e.target.value }))}
                placeholder="e.g. kube-system"
              />
            </div>
            <div className="flex items-end pb-1.5">
              <label className="flex items-center gap-2 text-sm cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.suppress_incident}
                  onChange={(e) => setForm((f) => ({ ...f, suppress_incident: e.target.checked }))}
                />
                Suppress incident creation
              </label>
            </div>
          </div>
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!form.name || createMutation.isPending}
              className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90 disabled:opacity-50"
            >
              {createMutation.isPending ? 'Saving…' : 'Save'}
            </button>
            <button onClick={() => setCreating(false)} className="px-3 py-1.5 text-sm border rounded-md hover:bg-accent">
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {rules?.length === 0 && !creating && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No alert routing rules yet.</p>
        </div>
      )}
      {rules && rules.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium w-8">#</th>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Matches</th>
                <th className="text-left px-4 py-2 font-medium">Action</th>
                <th className="text-left px-4 py-2 font-medium">Enabled</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {rules.map((rule: AlertRule) => {
                const matches = [
                  rule.match_alert_name && `alert=${rule.match_alert_name}`,
                  rule.match_severity && `severity=${rule.match_severity}`,
                  rule.match_namespace && `ns=${rule.match_namespace}`,
                  rule.match_label_key && `${rule.match_label_key}=${rule.match_label_value ?? '*'}`,
                ].filter(Boolean).join(', ')
                return (
                  <tr key={rule.id} className="hover:bg-muted/25">
                    <td className="px-4 py-2 text-muted-foreground font-mono">{rule.priority}</td>
                    <td className="px-4 py-2 font-medium">{rule.name}</td>
                    <td className="px-4 py-2 text-muted-foreground font-mono text-xs">{matches || '(any)'}</td>
                    <td className="px-4 py-2">
                      {rule.suppress_incident ? (
                        <span className="text-xs text-orange-600 dark:text-orange-400">suppress</span>
                      ) : rule.channel_id ? (
                        <span className="text-xs text-blue-600 dark:text-blue-400">route</span>
                      ) : (
                        <span className="text-xs text-muted-foreground">notify all</span>
                      )}
                    </td>
                    <td className="px-4 py-2">
                      <button
                        onClick={() => toggleMutation.mutate({ id: rule.id, enabled: !rule.is_enabled })}
                        className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${
                          rule.is_enabled ? 'bg-primary' : 'bg-muted-foreground/30'
                        }`}
                      >
                        <span
                          className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform ${
                            rule.is_enabled ? 'translate-x-4' : 'translate-x-1'
                          }`}
                        />
                      </button>
                    </td>
                    <td className="px-4 py-2 text-right">
                      <button
                        onClick={() => {
                          if (confirm(`Delete rule "${rule.name}"?`)) deleteMutation.mutate(rule.id)
                        }}
                        className="text-xs text-destructive hover:underline"
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// ────────────────────────────────────────────────────────────────
// Customers tab
// ────────────────────────────────────────────────────────────────

function CustomersTab({ tenantId }: { tenantId: string }) {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')

  const { data: customers, isLoading } = useQuery({
    queryKey: ['customers', tenantId],
    queryFn: () => customersApi.list(tenantId),
  })

  const createMutation = useMutation({
    mutationFn: () => customersApi.create(tenantId, name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['customers', tenantId] })
      setCreating(false)
      setName('')
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => customersApi.delete(tenantId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['customers', tenantId] }),
  })

  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        {!creating && (
          <button
            onClick={() => setCreating(true)}
            className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90"
          >
            Add customer
          </button>
        )}
      </div>

      {creating && (
        <div className="border rounded-lg p-4 bg-card space-y-3">
          <p className="text-sm font-medium">New customer</p>
          <input
            className="w-full border rounded px-2 py-1.5 text-sm bg-background"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Customer name"
          />
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!name || createMutation.isPending}
              className="px-3 py-1.5 text-sm bg-primary text-primary-foreground rounded-md hover:opacity-90 disabled:opacity-50"
            >
              {createMutation.isPending ? 'Creating…' : 'Create'}
            </button>
            <button
              onClick={() => setCreating(false)}
              className="px-3 py-1.5 text-sm border rounded-md hover:bg-accent"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {customers?.length === 0 && !creating && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No customers yet.</p>
        </div>
      )}
      {customers && customers.length > 0 && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Name</th>
                <th className="text-left px-4 py-2 font-medium">Created</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {customers.map((c: Customer) => (
                <tr key={c.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2 font-medium">{c.name}</td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {new Date(c.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      onClick={() => {
                        if (confirm(`Delete customer "${c.name}"?`)) {
                          deleteMutation.mutate(c.id)
                        }
                      }}
                      className="text-xs text-destructive hover:underline"
                    >
                      Delete
                    </button>
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
