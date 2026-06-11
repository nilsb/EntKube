import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { haApi, type HaNode } from '@/api/client'

export const Route = createFileRoute('/dashboard/ha')({
  component: HaPage,
})

function HaPage() {
  const { data: nodes, isLoading, error } = useQuery({
    queryKey: ['ha-nodes'],
    queryFn: haApi.listNodes,
    refetchInterval: 15_000,
  })

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold">HA Cluster</h1>
        <p className="text-sm text-muted-foreground">Registered cluster nodes and their sync status</p>
      </div>

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {error && <p className="text-sm text-destructive">Failed to load nodes</p>}

      {nodes && (
        <div className="border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Node ID</th>
                <th className="text-left px-4 py-2 font-medium">Address</th>
                <th className="text-left px-4 py-2 font-medium">Role</th>
                <th className="text-left px-4 py-2 font-medium">Last seen</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {nodes.map((node: HaNode) => (
                <tr key={node.id} className="hover:bg-muted/25">
                  <td className="px-4 py-2 font-mono text-xs">{node.id.slice(0, 8)}…</td>
                  <td className="px-4 py-2">{node.address}</td>
                  <td className="px-4 py-2">
                    {node.is_self ? (
                      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs bg-primary/10 text-primary font-medium">
                        This node
                      </span>
                    ) : (
                      <span className="text-muted-foreground">Peer</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {node.last_seen_at
                      ? new Date(node.last_seen_at).toLocaleString()
                      : '—'}
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
