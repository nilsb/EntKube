import { createFileRoute, Link, Outlet, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/authStore'
import { useQuery } from '@tanstack/react-query'
import { tenantsApi } from '@/api/client'

export const Route = createFileRoute('/dashboard')({
  beforeLoad() {
    const { accessToken, refreshToken } = useAuthStore.getState()
    if (!accessToken && !refreshToken) {
      throw redirect({ to: '/login' })
    }
  },
  component: DashboardLayout,
})

function DashboardLayout() {
  const { isAdmin, logout } = useAuthStore()
  const { data: tenants } = useQuery({
    queryKey: ['tenants'],
    queryFn: tenantsApi.list,
  })

  return (
    <div className="min-h-screen flex bg-background text-foreground">
      {/* Sidebar */}
      <aside className="w-56 border-r bg-card flex flex-col shrink-0">
        <div className="p-4 border-b">
          <span className="font-bold text-sm tracking-tight">EntKube</span>
        </div>
        <nav className="flex-1 p-2 space-y-1 text-sm overflow-y-auto">
          <Link
            to="/dashboard"
            className="flex items-center gap-2 rounded-md px-3 py-2 hover:bg-accent transition-colors"
            activeOptions={{ exact: true }}
            activeProps={{ className: 'bg-accent font-medium' }}
          >
            Overview
          </Link>

          {/* Per-tenant links */}
          {tenants && tenants.length > 0 && (
            <div className="pt-2">
              <p className="px-3 py-1 text-xs text-muted-foreground font-medium uppercase tracking-wide">
                Tenants
              </p>
              {tenants.map((t) => (
                <Link
                  key={t.id}
                  to="/dashboard/tenants/$tenantId"
                  params={{ tenantId: t.id }}
                  className="flex items-center gap-2 rounded-md px-3 py-2 hover:bg-accent transition-colors truncate"
                  activeProps={{ className: 'bg-accent font-medium' }}
                >
                  {t.name}
                </Link>
              ))}
            </div>
          )}

          <div className="pt-2">
            <p className="px-3 py-1 text-xs text-muted-foreground font-medium uppercase tracking-wide">
              Management
            </p>
            <Link
              to="/dashboard/tenants"
              className="flex items-center gap-2 rounded-md px-3 py-2 hover:bg-accent transition-colors"
              activeOptions={{ exact: true }}
              activeProps={{ className: 'bg-accent font-medium' }}
            >
              All Tenants
            </Link>
            {isAdmin && (
              <>
                <Link
                  to="/dashboard/users"
                  className="flex items-center gap-2 rounded-md px-3 py-2 hover:bg-accent transition-colors"
                  activeProps={{ className: 'bg-accent font-medium' }}
                >
                  Users
                </Link>
                <Link
                  to="/dashboard/ha"
                  className="flex items-center gap-2 rounded-md px-3 py-2 hover:bg-accent transition-colors"
                  activeProps={{ className: 'bg-accent font-medium' }}
                >
                  HA Cluster
                </Link>
              </>
            )}
          </div>
        </nav>
        <div className="p-2 border-t">
          <button
            onClick={logout}
            className="w-full text-left text-sm rounded-md px-3 py-2 hover:bg-accent transition-colors text-muted-foreground"
          >
            Sign out
          </button>
        </div>
      </aside>

      {/* Main content */}
      <main className="flex-1 p-6 overflow-auto">
        <Outlet />
      </main>
    </div>
  )
}
