import { useAuthStore } from '@/stores/authStore'

const BASE = import.meta.env.VITE_API_BASE ?? ''

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = useAuthStore.getState().accessToken
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init.headers as Record<string, string>),
  }
  if (token) headers['Authorization'] = `Bearer ${token}`

  const res = await fetch(`${BASE}${path}`, { ...init, headers })

  // Transparently refresh access token on 401 and retry once.
  if (res.status === 401 && useAuthStore.getState().refreshToken) {
    const ok = await useAuthStore.getState().refresh()
    if (ok) {
      const newToken = useAuthStore.getState().accessToken
      if (newToken) headers['Authorization'] = `Bearer ${newToken}`
      const retry = await fetch(`${BASE}${path}`, { ...init, headers })
      if (!retry.ok) throw new ApiError(retry.status, await retry.text())
      if (retry.status === 204) return undefined as T
      return retry.json() as Promise<T>
    }
    useAuthStore.getState().logout()
    throw new ApiError(401, 'Session expired')
  }

  if (!res.ok) throw new ApiError(res.status, await res.text())
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

export const api = {
  get:    <T>(path: string)                    => request<T>(path),
  post:   <T>(path: string, body?: unknown)    => request<T>(path, { method: 'POST',   body: JSON.stringify(body) }),
  put:    <T>(path: string, body?: unknown)    => request<T>(path, { method: 'PUT',    body: JSON.stringify(body) }),
  patch:  <T>(path: string, body?: unknown)    => request<T>(path, { method: 'PATCH',  body: JSON.stringify(body) }),
  delete: <T>(path: string)                    => request<T>(path, { method: 'DELETE' }),
}

// ────────────────────────────────────────────────────────────────
// Auth
// ────────────────────────────────────────────────────────────────

export interface TokenPair {
  access_token:  string
  refresh_token: string
  expires_at:    string
}

export const authApi = {
  login:   (email: string, password: string) =>
    api.post<TokenPair>('/api/auth/login', { email, password }),
  refresh: (refresh_token: string) =>
    api.post<TokenPair>('/api/auth/refresh', { refresh_token }),
  logout:  (refresh_token: string) =>
    api.post<void>('/api/auth/logout', { refresh_token }),
  me: () => api.get<{ user_id: string; is_admin: boolean }>('/api/auth/me'),
}

// ────────────────────────────────────────────────────────────────
// HA nodes
// ────────────────────────────────────────────────────────────────

export interface HaNode {
  id:           string
  address:      string
  is_self:      boolean
  last_seen_at: string | null
}

export const haApi = {
  listNodes: () => api.get<HaNode[]>('/api/ha/nodes'),
}

// ────────────────────────────────────────────────────────────────
// Users
// ────────────────────────────────────────────────────────────────

export interface User {
  id:         string
  email:      string
  is_admin:   boolean
  created_at: string
  deleted_at?: string | null
}

export const usersApi = {
  list:   () => api.get<User[]>('/api/users'),
  get:    (id: string) => api.get<User>(`/api/users/${id}`),
  create: (email: string, password: string, is_admin: boolean) =>
    api.post<User>('/api/users', { email, password, is_admin }),
  update: (id: string, patch: { is_admin?: boolean; password?: string }) =>
    api.patch<void>(`/api/users/${id}`, patch),
  delete: (id: string) => api.delete<void>(`/api/users/${id}`),
}

// ────────────────────────────────────────────────────────────────
// Tenants
// ────────────────────────────────────────────────────────────────

export interface Tenant {
  id:         string
  name:       string
  slug:       string
  created_at: string
}

export const tenantsApi = {
  list:   () => api.get<Tenant[]>('/api/tenants'),
  get:    (id: string) => api.get<Tenant>(`/api/tenants/${id}`),
  create: (name: string, slug: string) => api.post<Tenant>('/api/tenants', { name, slug }),
  delete: (id: string) => api.delete<void>(`/api/tenants/${id}`),
  addMember: (tenantId: string, userId: string, role: string) =>
    api.post<void>(`/api/tenants/${tenantId}/members`, { user_id: userId, role }),
  removeMember: (tenantId: string, userId: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/members/${userId}`),
}

// ────────────────────────────────────────────────────────────────
// Environments
// ────────────────────────────────────────────────────────────────

export interface Environment {
  id:         string
  tenant_id:  string
  name:       string
  created_at: string
}

export const environmentsApi = {
  list:   (tenantId: string) => api.get<Environment[]>(`/api/tenants/${tenantId}/environments`),
  create: (tenantId: string, name: string) =>
    api.post<Environment>(`/api/tenants/${tenantId}/environments`, { name }),
  delete: (tenantId: string, envId: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/environments/${envId}`),
}

// ────────────────────────────────────────────────────────────────
// Customers
// ────────────────────────────────────────────────────────────────

export interface Customer {
  id:         string
  tenant_id:  string
  name:       string
  created_at: string
}

export const customersApi = {
  list:   (tenantId: string) => api.get<Customer[]>(`/api/tenants/${tenantId}/customers`),
  get:    (tenantId: string, id: string) => api.get<Customer>(`/api/tenants/${tenantId}/customers/${id}`),
  create: (tenantId: string, name: string) =>
    api.post<Customer>(`/api/tenants/${tenantId}/customers`, { name }),
  delete: (tenantId: string, id: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/customers/${id}`),
}

// ────────────────────────────────────────────────────────────────
// Apps
// ────────────────────────────────────────────────────────────────

export interface App {
  id:          string
  customer_id: string
  name:        string
  namespace?:  string | null
  created_at:  string
}

export const appsApi = {
  list:   (tenantId: string, customerId: string) =>
    api.get<App[]>(`/api/tenants/${tenantId}/customers/${customerId}/apps`),
  create: (tenantId: string, customerId: string, name: string, namespace?: string) =>
    api.post<App>(`/api/tenants/${tenantId}/customers/${customerId}/apps`, { name, namespace }),
  delete: (tenantId: string, customerId: string, appId: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/customers/${customerId}/apps/${appId}`),
}

// ────────────────────────────────────────────────────────────────
// Vault secrets
// ────────────────────────────────────────────────────────────────

export interface Secret {
  id:         string
  key_name:   string
  value:      string
  updated_at: string
}

export const vaultApi = {
  list:   (tenantId: string) => api.get<Secret[]>(`/api/tenants/${tenantId}/secrets`),
  upsert: (tenantId: string, keyName: string, value: string) =>
    api.put<void>(`/api/tenants/${tenantId}/secrets/${keyName}`, { value }),
  delete: (tenantId: string, keyName: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/secrets/${keyName}`),
}

// ────────────────────────────────────────────────────────────────
// Kubernetes clusters
// ────────────────────────────────────────────────────────────────

export interface Cluster {
  id:             string
  tenant_id:      string
  environment_id: string
  name:           string
  api_server_url: string
  created_at:     string
}

export const clustersApi = {
  list:   (tenantId: string) => api.get<Cluster[]>(`/api/tenants/${tenantId}/clusters`),
  create: (tenantId: string, body: {
    environment_id: string
    name:           string
    api_server_url: string
    kubeconfig?:    string
  }) => api.post<Cluster>(`/api/tenants/${tenantId}/clusters`, body),
  listNamespaces: (tenantId: string, clusterId: string) =>
    api.get<string[]>(`/api/tenants/${tenantId}/clusters/${clusterId}/namespaces`),
}

// ────────────────────────────────────────────────────────────────
// Deployments
// ────────────────────────────────────────────────────────────────

export interface Deployment {
  id:                string
  app_id:            string
  name:              string
  deployment_type:   string
  namespace:         string
  cluster_id:        string
  sync_status:       string
  health_status:     string
  status_message?:   string | null
  last_synced_at?:   string | null
  git_url?:          string | null
  git_revision:      string
  git_auto_sync:     boolean
  helm_chart_name?:  string | null
  helm_chart_version?: string | null
  created_at:        string
}

export const deploymentsApi = {
  list:   (tenantId: string) => api.get<Deployment[]>(`/api/tenants/${tenantId}/deployments`),
  create: (tenantId: string, body: {
    app_id:           string
    cluster_id:       string
    environment_id:   string
    name:             string
    deployment_type?: string
    namespace:        string
    git_url?:         string
    git_revision?:    string
    git_path?:        string
    git_auto_sync?:   boolean
  }) => api.post<Deployment>(`/api/tenants/${tenantId}/deployments`, body),
  triggerSync: (tenantId: string, deploymentId: string) =>
    api.post<void>(`/api/tenants/${tenantId}/deployments/${deploymentId}/sync`),
}

// ────────────────────────────────────────────────────────────────
// Git repositories
// ────────────────────────────────────────────────────────────────

export interface GitRepo {
  id:             string
  tenant_id:      string
  name:           string
  url:            string
  auth_type:      string
  username?:      string | null
  default_branch: string
  created_at:     string
}

export const gitReposApi = {
  list:   (tenantId: string) => api.get<GitRepo[]>(`/api/tenants/${tenantId}/git-repos`),
  create: (tenantId: string, body: {
    name:           string
    url:            string
    auth_type?:     string
    username?:      string
    credential?:    string
    default_branch?: string
  }) => api.post<GitRepo>(`/api/tenants/${tenantId}/git-repos`, body),
  delete: (tenantId: string, repoId: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/git-repos/${repoId}`),
}

// ────────────────────────────────────────────────────────────────
// Notification channels
// ────────────────────────────────────────────────────────────────

export interface NotificationChannel {
  id:              string
  tenant_id:       string
  name:            string
  channel_type:    string
  config_json:     string
  is_enabled:      boolean
  severity_filter: string
  created_at:      string
}

export const notifApi = {
  list:   (tenantId: string) =>
    api.get<NotificationChannel[]>(`/api/tenants/${tenantId}/notification-channels`),
  create: (tenantId: string, body: {
    name:            string
    channel_type:    string
    config:          Record<string, string>
    severity_filter?: string
  }) => api.post<NotificationChannel>(`/api/tenants/${tenantId}/notification-channels`, body),
  delete: (tenantId: string, channelId: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/notification-channels/${channelId}`),
  test: (tenantId: string, channelId: string) =>
    api.post<{ status: string; message?: string }>(
      `/api/tenants/${tenantId}/notification-channels/${channelId}/test`
    ),
}

// ────────────────────────────────────────────────────────────────
// Cluster components
// ────────────────────────────────────────────────────────────────

export interface ClusterComponent {
  id:                  string
  cluster_id:          string
  name:                string
  helm_chart_name?:    string | null
  helm_repo_url?:      string | null
  helm_chart_version?: string | null
  release_name?:       string | null
  helm_values?:        string | null
  namespace?:          string | null
  status:              string
  last_error?:         string | null
  installed_at?:       string | null
  created_at:          string
  updated_at:          string
}

export const componentsApi = {
  list: (tenantId: string, clusterId: string) =>
    api.get<ClusterComponent[]>(`/api/tenants/${tenantId}/clusters/${clusterId}/components`),
  create: (tenantId: string, clusterId: string, body: {
    name: string
    helm_chart_name?: string
    helm_repo_url?: string
    helm_chart_version?: string
    release_name?: string
    helm_values?: string
    namespace?: string
  }) => api.post<ClusterComponent>(`/api/tenants/${tenantId}/clusters/${clusterId}/components`, body),
  updateStatus: (tenantId: string, clusterId: string, componentId: string, status: string, last_error?: string) =>
    api.patch<void>(`/api/tenants/${tenantId}/clusters/${clusterId}/components/${componentId}`, { status, last_error }),
  delete: (tenantId: string, clusterId: string, componentId: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/clusters/${clusterId}/components/${componentId}`),
}

// ────────────────────────────────────────────────────────────────
// Maintenance windows
// ────────────────────────────────────────────────────────────────

export interface MaintenanceWindow {
  id:         string
  tenant_id:  string
  name:       string
  starts_at:  string
  ends_at:    string
  is_active:  boolean
  created_at: string
}

export const maintenanceApi = {
  list: (tenantId: string) =>
    api.get<MaintenanceWindow[]>(`/api/tenants/${tenantId}/maintenance-windows`),
  create: (tenantId: string, body: { name: string; starts_at: string; ends_at: string }) =>
    api.post<MaintenanceWindow>(`/api/tenants/${tenantId}/maintenance-windows`, body),
  delete: (tenantId: string, windowId: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/maintenance-windows/${windowId}`),
}

// ────────────────────────────────────────────────────────────────
// Alert routing rules
// ────────────────────────────────────────────────────────────────

export interface AlertRule {
  id:                string
  tenant_id:         string
  name:              string
  priority:          number
  is_enabled:        boolean
  suppress_incident: boolean
  match_alert_name?: string | null
  match_namespace?:  string | null
  match_severity?:   string | null
  match_label_key?:  string | null
  match_label_value?: string | null
  match_cluster_id?: string | null
  channel_id?:       string | null
  created_at:        string
  updated_at:        string
}

export const alertRulesApi = {
  list: (tenantId: string) =>
    api.get<AlertRule[]>(`/api/tenants/${tenantId}/alert-rules`),
  create: (tenantId: string, body: {
    name:              string
    priority?:         number
    suppress_incident?: boolean
    match_alert_name?: string
    match_namespace?:  string
    match_severity?:   string
    match_label_key?:  string
    match_label_value?: string
    match_cluster_id?: string
    channel_id?:       string
  }) => api.post<AlertRule>(`/api/tenants/${tenantId}/alert-rules`, body),
  toggleEnabled: (tenantId: string, ruleId: string, is_enabled: boolean) =>
    api.patch<void>(`/api/tenants/${tenantId}/alert-rules/${ruleId}`, { is_enabled }),
  delete: (tenantId: string, ruleId: string) =>
    api.delete<void>(`/api/tenants/${tenantId}/alert-rules/${ruleId}`),
}

// ────────────────────────────────────────────────────────────────
// Alert incidents
// ────────────────────────────────────────────────────────────────

export interface AlertIncident {
  id:               string
  cluster_id:       string
  cluster_name:     string
  fingerprint:      string
  alert_name:       string
  severity:         string
  summary:          string
  description:      string
  runbook_url:      string
  labels_json:      string
  starts_at:        string
  ends_at?:         string | null
  status:           string
  acknowledged_by?: string | null
  acknowledged_at?: string | null
  resolved_at?:     string | null
  escalated_at?:    string | null
  created_at:       string
  updated_at:       string
}

export const alertsApi = {
  list: (tenantId: string, params?: { status?: string; severity?: string }) => {
    const qs = new URLSearchParams()
    if (params?.status) qs.set('status', params.status)
    if (params?.severity) qs.set('severity', params.severity)
    const q = qs.toString()
    return api.get<AlertIncident[]>(`/api/tenants/${tenantId}/alerts${q ? '?' + q : ''}`)
  },
  acknowledge: (tenantId: string, alertId: string) =>
    api.post<void>(`/api/tenants/${tenantId}/alerts/${alertId}/acknowledge`),
  resolve: (tenantId: string, alertId: string) =>
    api.post<void>(`/api/tenants/${tenantId}/alerts/${alertId}/resolve`),
  listNotes: (tenantId: string, alertId: string) =>
    api.get<{ id: string; author: string; body: string; created_at: string }[]>(
      `/api/tenants/${tenantId}/alerts/${alertId}/notes`
    ),
  addNote: (tenantId: string, alertId: string, body: string) =>
    api.post<void>(`/api/tenants/${tenantId}/alerts/${alertId}/notes`, { body }),
}
