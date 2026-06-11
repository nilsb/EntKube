package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvironmentHandler serves /api/tenants/{tenantID}/environments/*.
type EnvironmentHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewEnvironmentHandler creates an EnvironmentHandler.
func NewEnvironmentHandler(pool *pgxpool.Pool, selfNode uuid.UUID) *EnvironmentHandler {
	return &EnvironmentHandler{pool: pool, selfNode: selfNode}
}

type environmentDTO struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// ── GET /api/tenants/{tenantID}/environments ─────────────────────

func (h *EnvironmentHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT id, tenant_id, name, created_at
		 FROM   environments
		 WHERE  tenant_id = $1
		 ORDER  BY name`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var envs []environmentDTO
	for rows.Next() {
		var e environmentDTO
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Name, &e.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		envs = append(envs, e)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, envs)
}

// ── POST /api/tenants/{tenantID}/environments ────────────────────

func (h *EnvironmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	var e environmentDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO environments (tenant_id, name, origin_node_id)
		VALUES ($1, $2, $3)
		RETURNING id, tenant_id, name, created_at`,
		tenantID, body.Name, h.selfNode).
		Scan(&e.ID, &e.TenantID, &e.Name, &e.CreatedAt)
	if err != nil {
		http.Error(w, "could not create environment (name may already exist)", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// ── DELETE /api/tenants/{tenantID}/environments/{envID} ──────────

func (h *EnvironmentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	envID, err := uuid.Parse(r.PathValue("envID"))
	if err != nil {
		http.Error(w, "invalid envID", http.StatusBadRequest)
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM environments WHERE id = $1 AND tenant_id = $2`, envID, tenantID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
