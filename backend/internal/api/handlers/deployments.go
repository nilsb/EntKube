package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeploymentHandler serves /api/tenants/{tenantID}/deployments/*.
type DeploymentHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
	// gitSyncQueue allows API handlers to trigger on-demand git syncs.
	gitSyncQueue func(uuid.UUID)
}

// NewDeploymentHandler creates a DeploymentHandler.
func NewDeploymentHandler(pool *pgxpool.Pool, selfNode uuid.UUID, gitSyncQueue func(uuid.UUID)) *DeploymentHandler {
	return &DeploymentHandler{pool: pool, selfNode: selfNode, gitSyncQueue: gitSyncQueue}
}

type deploymentDTO struct {
	ID              uuid.UUID  `json:"id"`
	AppID           uuid.UUID  `json:"app_id"`
	Name            string     `json:"name"`
	DeploymentType  string     `json:"deployment_type"`
	Namespace       string     `json:"namespace"`
	ClusterID       uuid.UUID  `json:"cluster_id"`
	SyncStatus      string     `json:"sync_status"`
	HealthStatus    string     `json:"health_status"`
	StatusMessage   *string    `json:"status_message,omitempty"`
	LastSyncedAt    *time.Time `json:"last_synced_at,omitempty"`
	GitURL          *string    `json:"git_url,omitempty"`
	GitRevision     string     `json:"git_revision"`
	GitAutoSync     bool       `json:"git_auto_sync"`
	HelmChartName   *string    `json:"helm_chart_name,omitempty"`
	HelmChartVersion *string   `json:"helm_chart_version,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// ── GET /api/tenants/{tenantID}/deployments ──────────────────────

func (h *DeploymentHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT d.id, d.app_id, d.name, d.deployment_type::text, d.namespace,
		       d.cluster_id, d.sync_status::text, d.health_status::text,
		       d.status_message, d.last_synced_at,
		       d.git_url, d.git_revision, d.git_auto_sync,
		       d.helm_chart_name, d.helm_chart_version,
		       d.created_at
		FROM   app_deployments d
		JOIN   apps a ON a.id = d.app_id
		JOIN   customers cu ON cu.id = a.customer_id
		WHERE  cu.tenant_id = $1
		ORDER  BY d.created_at DESC`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var deployments []deploymentDTO
	for rows.Next() {
		var d deploymentDTO
		if err := rows.Scan(
			&d.ID, &d.AppID, &d.Name, &d.DeploymentType, &d.Namespace,
			&d.ClusterID, &d.SyncStatus, &d.HealthStatus,
			&d.StatusMessage, &d.LastSyncedAt,
			&d.GitURL, &d.GitRevision, &d.GitAutoSync,
			&d.HelmChartName, &d.HelmChartVersion,
			&d.CreatedAt,
		); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		deployments = append(deployments, d)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, deployments)
}

// ── POST /api/tenants/{tenantID}/deployments ─────────────────────

func (h *DeploymentHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	var body struct {
		AppID          uuid.UUID `json:"app_id"`
		ClusterID      uuid.UUID `json:"cluster_id"`
		EnvironmentID  uuid.UUID `json:"environment_id"`
		Name           string    `json:"name"`
		DeploymentType string    `json:"deployment_type"`
		Namespace      string    `json:"namespace"`
		GitURL         string    `json:"git_url"`
		GitRevision    string    `json:"git_revision"`
		GitPath        string    `json:"git_path"`
		GitAutoSync    bool      `json:"git_auto_sync"`
		HelmRepoURL    string    `json:"helm_repo_url"`
		HelmChartName  string    `json:"helm_chart_name"`
		HelmChartVersion string  `json:"helm_chart_version"`
		HelmValues     string    `json:"helm_values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Name == "" || body.Namespace == "" || body.AppID == uuid.Nil {
		http.Error(w, "app_id, name and namespace required", http.StatusBadRequest)
		return
	}

	// Verify the app belongs to this tenant.
	var exists bool
	_ = h.pool.QueryRow(r.Context(), `
		SELECT EXISTS(
			SELECT 1 FROM apps a JOIN customers cu ON cu.id = a.customer_id
			WHERE a.id = $1 AND cu.tenant_id = $2)`,
		body.AppID, tenantID).Scan(&exists)
	if !exists {
		http.Error(w, "app not found in tenant", http.StatusNotFound)
		return
	}

	if body.DeploymentType == "" {
		body.DeploymentType = "manual"
	}
	if body.GitRevision == "" {
		body.GitRevision = "main"
	}
	if body.GitPath == "" {
		body.GitPath = "."
	}

	var d deploymentDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO app_deployments
			(app_id, environment_id, cluster_id, name, deployment_type, namespace,
			 git_url, git_revision, git_path, git_auto_sync,
			 helm_repo_url, helm_chart_name, helm_chart_version, helm_values,
			 origin_node_id)
		VALUES ($1,$2,$3,$4,$5::deployment_type,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, app_id, name, deployment_type::text, namespace, cluster_id,
		          sync_status::text, health_status::text, status_message, last_synced_at,
		          git_url, git_revision, git_auto_sync, helm_chart_name, helm_chart_version, created_at`,
		body.AppID, body.EnvironmentID, body.ClusterID, body.Name, body.DeploymentType, body.Namespace,
		nullableStr(body.GitURL, body.GitURL != ""),
		body.GitRevision, body.GitPath, body.GitAutoSync,
		nullableStr(body.HelmRepoURL, body.HelmRepoURL != ""),
		nullableStr(body.HelmChartName, body.HelmChartName != ""),
		nullableStr(body.HelmChartVersion, body.HelmChartVersion != ""),
		nullableStr(body.HelmValues, body.HelmValues != ""),
		h.selfNode).
		Scan(&d.ID, &d.AppID, &d.Name, &d.DeploymentType, &d.Namespace, &d.ClusterID,
			&d.SyncStatus, &d.HealthStatus, &d.StatusMessage, &d.LastSyncedAt,
			&d.GitURL, &d.GitRevision, &d.GitAutoSync, &d.HelmChartName, &d.HelmChartVersion, &d.CreatedAt)
	if err != nil {
		http.Error(w, "could not create deployment: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, d)
}

// ── POST /api/tenants/{tenantID}/deployments/{deploymentID}/sync ─

// TriggerSync enqueues an immediate git sync for a deployment (webhook / manual).
func (h *DeploymentHandler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("deploymentID")
	id, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid deploymentID", http.StatusBadRequest)
		return
	}
	if h.gitSyncQueue != nil {
		h.gitSyncQueue(id)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

// ── GET /api/tenants/{tenantID}/deployments/{deploymentID}/resources ─

func (h *DeploymentHandler) ListResources(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("deploymentID")
	id, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid deploymentID", http.StatusBadRequest)
		return
	}

	type resourceDTO struct {
		ID             uuid.UUID  `json:"id"`
		Kind           string     `json:"kind"`
		Name           string     `json:"name"`
		Namespace      *string    `json:"namespace,omitempty"`
		SyncStatus     string     `json:"sync_status"`
		HealthStatus   string     `json:"health_status"`
		StatusMessage  *string    `json:"status_message,omitempty"`
		ParentID       *uuid.UUID `json:"parent_id,omitempty"`
		LastUpdatedAt  time.Time  `json:"last_updated_at"`
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, kind, name, namespace,
		       sync_status::text, health_status::text, status_message,
		       parent_resource_id, last_updated_at
		FROM   deployment_resources
		WHERE  deployment_id = $1
		ORDER  BY kind, name`, id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var resources []resourceDTO
	for rows.Next() {
		var res resourceDTO
		if err := rows.Scan(&res.ID, &res.Kind, &res.Name, &res.Namespace,
			&res.SyncStatus, &res.HealthStatus, &res.StatusMessage,
			&res.ParentID, &res.LastUpdatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		resources = append(resources, res)
	}
	writeJSON(w, http.StatusOK, resources)
}
