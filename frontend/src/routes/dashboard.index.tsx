import { createFileRoute, Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { tenantsApi, deploymentsApi, alertsApi, clustersApi } from '@/api/client'

export const Route = createFileRoute('/dashboard/')({
  component: OverviewPage,
})

function StatCard({ label, value, sub }: { label: string; value: string | number; sub?: string }) {
  return (
    <div className="border rounded-lg p-4 bg-card">
      <p className="text-sm text-muted-foreground">{label}</p>
      <p className="text-2xl font-bold mt-1">{value}</p>
      {sub && <p className="text-xs text-muted-foreground mt-1">{sub}</p>}
    </div>
  )
}

function OverviewPage() {
  const { data: tenants } = useQuery({ queryKey: ['tenants'], queryFn: tenantsApi.list })

  // Aggregate stats across all tenants the user can see
  const tenantIds = tenants?.map((t) => t.id) ?? []
  const firstTenantId = tenantIds[0]
  const { data: sampleDeployments } = useQuery({
    queryKey: ['deployments', firstTenantId],
    queryFn: () => deploymentsApi.list(firstTenantId!),
    enabled: !!firstTenantId,
  })
  const { data: sampleClusters } = useQuery({
    queryKey: ['clusters', firstTenantId],
    queryFn: () => clustersApi.list(firstTenantId!),
    enabled: !!firstTenantId,
  })
  const { data: sampleAlerts } = useQuery({
    queryKey: ['alerts', firstTenantId, 'active'],
    queryFn: () => alertsApi.list(firstTenantId!, { status: 'active' }),
    enabled: !!firstTenantId,
  })

  const healthyDeployments = sampleDeployments?.filter(
    (d) => d.health_status === 'healthy',
  ).length ?? 0
  const totalDeployments = sampleDeployments?.length ?? 0
  const activeAlerts = sampleAlerts?.filter((a) => a.severity === 'critical').length ?? 0

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold">Overview</h1>
        <p className="text-sm text-muted-foreground">
          {tenants?.length ?? 0} accessible tenant{(tenants?.length ?? 0) !== 1 ? 's' : ''}
        </p>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <StatCard label="Tenants" value={tenants?.length ?? '—'} />
        <StatCard
          label="Clusters"
          value={sampleClusters?.length ?? '—'}
          sub={firstTenantId ? 'first tenant' : undefined}
        />
        <StatCard
          label="Deployments"
          value={totalDeployments}
          sub={`${healthyDeployments} healthy`}
        />
        <StatCard
          label="Critical alerts"
          value={activeAlerts}
          sub="active"
        />
      </div>

      {tenants && tenants.length > 0 && (
        <div>
          <h2 className="text-sm font-medium mb-3">Quick access</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
            {tenants.map((t) => (
              <Link
                key={t.id}
                to="/dashboard/tenants/$tenantId"
                params={{ tenantId: t.id }}
                className="border rounded-lg p-4 bg-card hover:bg-accent transition-colors group"
              >
                <p className="font-medium group-hover:underline">{t.name}</p>
                <p className="text-xs text-muted-foreground mt-1">{t.slug}</p>
              </Link>
            ))}
          </div>
        </div>
      )}

      {tenants && tenants.length === 0 && (
        <div className="border rounded-lg p-8 text-center bg-card">
          <p className="text-muted-foreground text-sm">No tenants available.</p>
          <p className="text-xs text-muted-foreground mt-1">
            Ask an admin to create a tenant and add you as a member.
          </p>
        </div>
      )}
    </div>
  )
}
