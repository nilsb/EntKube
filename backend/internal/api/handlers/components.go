package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ComponentHandler serves /api/tenants/{tenantID}/clusters/{clusterID}/components/*.
type ComponentHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewComponentHandler creates a ComponentHandler.
func NewComponentHandler(pool *pgxpool.Pool, selfNode uuid.UUID) *ComponentHandler {
	return &ComponentHandler{pool: pool, selfNode: selfNode}
}

type componentDTO struct {
	ID               uuid.UUID  `json:"id"`
	ClusterID        uuid.UUID  `json:"cluster_id"`
	Name             string     `json:"name"`
	HelmChartName    *string    `json:"helm_chart_name,omitempty"`
	HelmRepoURL      *string    `json:"helm_repo_url,omitempty"`
	HelmChartVersion *string    `json:"helm_chart_version,omitempty"`
	ReleaseName      *string    `json:"release_name,omitempty"`
	HelmValues       *string    `json:"helm_values,omitempty"`
	Namespace        *string    `json:"namespace,omitempty"`
	Status           string     `json:"status"`
	LastError        *string    `json:"last_error,omitempty"`
	InstalledAt      *time.Time `json:"installed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ── GET /api/tenants/{tenantID}/clusters/{clusterID}/components ──

func (h *ComponentHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(r, w)
	if !ok {
		return
	}
	if !h.clusterInTenant(r, clusterID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, cluster_id, name,
		       helm_chart_name, helm_repo_url, helm_chart_version,
		       release_name, helm_values, namespace,
		       status::text, last_error, installed_at,
		       created_at, updated_at
		FROM   cluster_components
		WHERE  cluster_id = $1
		ORDER  BY name`, clusterID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var comps []componentDTO
	for rows.Next() {
		var c componentDTO
		if err := rows.Scan(
			&c.ID, &c.ClusterID, &c.Name,
			&c.HelmChartName, &c.HelmRepoURL, &c.HelmChartVersion,
			&c.ReleaseName, &c.HelmValues, &c.Namespace,
			&c.Status, &c.LastError, &c.InstalledAt,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		comps = append(comps, c)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, comps)
}

// ── POST /api/tenants/{tenantID}/clusters/{clusterID}/components ─

func (h *ComponentHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(r, w)
	if !ok {
		return
	}
	if !h.clusterInTenant(r, clusterID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var body struct {
		Name             string `json:"name"`
		HelmChartName    string `json:"helm_chart_name"`
		HelmRepoURL      string `json:"helm_repo_url"`
		HelmChartVersion string `json:"helm_chart_version"`
		ReleaseName      string `json:"release_name"`
		HelmValues       string `json:"helm_values"`
		Namespace        string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	var c componentDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO cluster_components
			(cluster_id, name, helm_chart_name, helm_repo_url, helm_chart_version,
			 release_name, helm_values, namespace, origin_node_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, cluster_id, name,
		          helm_chart_name, helm_repo_url, helm_chart_version,
		          release_name, helm_values, namespace,
		          status::text, last_error, installed_at,
		          created_at, updated_at`,
		clusterID, body.Name,
		nullableStr(body.HelmChartName, body.HelmChartName != ""),
		nullableStr(body.HelmRepoURL, body.HelmRepoURL != ""),
		nullableStr(body.HelmChartVersion, body.HelmChartVersion != ""),
		nullableStr(body.ReleaseName, body.ReleaseName != ""),
		nullableStr(body.HelmValues, body.HelmValues != ""),
		nullableStr(body.Namespace, body.Namespace != ""),
		h.selfNode).
		Scan(&c.ID, &c.ClusterID, &c.Name,
			&c.HelmChartName, &c.HelmRepoURL, &c.HelmChartVersion,
			&c.ReleaseName, &c.HelmValues, &c.Namespace,
			&c.Status, &c.LastError, &c.InstalledAt,
			&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		http.Error(w, "could not create component: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// ── PATCH /api/tenants/{tenantID}/clusters/{clusterID}/components/{componentID} ──
// Updates status (e.g. mark installed/failed after Helm operation).

func (h *ComponentHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(r, w)
	if !ok {
		return
	}
	if !h.clusterInTenant(r, clusterID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	componentID, ok := parseComponentID(r, w)
	if !ok {
		return
	}

	var body struct {
		Status    string  `json:"status"`
		LastError *string `json:"last_error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Status == "" {
		http.Error(w, "status required", http.StatusBadRequest)
		return
	}

	installedClause := ""
	if body.Status == "installed" {
		installedClause = ", installed_at = COALESCE(installed_at, now())"
	}

	_, err := h.pool.Exec(r.Context(), `
		UPDATE cluster_components
		SET    status     = $2::component_status,
		       last_error = $3,
		       updated_at = now()`+installedClause+`
		WHERE  id = $1 AND cluster_id = $4`,
		componentID, body.Status, body.LastError, clusterID)
	if err != nil {
		http.Error(w, "could not update status: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── DELETE /api/tenants/{tenantID}/clusters/{clusterID}/components/{componentID} ──

func (h *ComponentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(r, w)
	if !ok {
		return
	}
	if !h.clusterInTenant(r, clusterID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	componentID, ok := parseComponentID(r, w)
	if !ok {
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM cluster_components WHERE id = $1 AND cluster_id = $2`,
		componentID, clusterID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ComponentHandler) clusterInTenant(r *http.Request, clusterID, tenantID uuid.UUID) bool {
	var exists bool
	_ = h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM kubernetes_clusters WHERE id = $1 AND tenant_id = $2)`,
		clusterID, tenantID).Scan(&exists)
	return exists
}

func parseComponentID(r *http.Request, w http.ResponseWriter) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("componentID"))
	if err != nil {
		http.Error(w, "invalid componentID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}
