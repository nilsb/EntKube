package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaintenanceHandler serves /api/tenants/{tenantID}/maintenance-windows/*.
type MaintenanceHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewMaintenanceHandler creates a MaintenanceHandler.
func NewMaintenanceHandler(pool *pgxpool.Pool, selfNode uuid.UUID) *MaintenanceHandler {
	return &MaintenanceHandler{pool: pool, selfNode: selfNode}
}

type maintenanceWindowDTO struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	Name      string    `json:"name"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// ── GET /api/tenants/{tenantID}/maintenance-windows ──────────────

func (h *MaintenanceHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, tenant_id, name, starts_at, ends_at,
		       (starts_at <= now() AND ends_at >= now()) AS is_active,
		       created_at
		FROM   maintenance_windows
		WHERE  tenant_id = $1
		ORDER  BY starts_at DESC
		LIMIT  100`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var windows []maintenanceWindowDTO
	for rows.Next() {
		var mw maintenanceWindowDTO
		if err := rows.Scan(&mw.ID, &mw.TenantID, &mw.Name,
			&mw.StartsAt, &mw.EndsAt, &mw.IsActive, &mw.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		windows = append(windows, mw)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, windows)
}

// ── POST /api/tenants/{tenantID}/maintenance-windows ─────────────

func (h *MaintenanceHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	var body struct {
		Name     string    `json:"name"`
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Name == "" || body.StartsAt.IsZero() || body.EndsAt.IsZero() {
		http.Error(w, "name, starts_at and ends_at required", http.StatusBadRequest)
		return
	}
	if !body.EndsAt.After(body.StartsAt) {
		http.Error(w, "ends_at must be after starts_at", http.StatusBadRequest)
		return
	}

	var mw maintenanceWindowDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO maintenance_windows (tenant_id, name, starts_at, ends_at, origin_node_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, name, starts_at, ends_at,
		          (starts_at <= now() AND ends_at >= now()), created_at`,
		tenantID, body.Name, body.StartsAt, body.EndsAt, h.selfNode).
		Scan(&mw.ID, &mw.TenantID, &mw.Name, &mw.StartsAt, &mw.EndsAt, &mw.IsActive, &mw.CreatedAt)
	if err != nil {
		http.Error(w, "could not create window: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, mw)
}

// ── DELETE /api/tenants/{tenantID}/maintenance-windows/{windowID} ─

func (h *MaintenanceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	windowID, err := uuid.Parse(r.PathValue("windowID"))
	if err != nil {
		http.Error(w, "invalid windowID", http.StatusBadRequest)
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM maintenance_windows WHERE id = $1 AND tenant_id = $2`,
		windowID, tenantID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
