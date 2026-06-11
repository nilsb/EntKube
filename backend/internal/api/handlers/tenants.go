package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	authpkg "github.com/entkube/entkube/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantHandler serves /api/tenants/*.
type TenantHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewTenantHandler creates a TenantHandler.
func NewTenantHandler(pool *pgxpool.Pool, selfNode uuid.UUID) *TenantHandler {
	return &TenantHandler{pool: pool, selfNode: selfNode}
}

// ── GET /api/tenants ─────────────────────────────────────────────

func (h *TenantHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := authpkg.ClaimsFromCtx(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var rows interface{}
	var err error

	if claims.IsAdmin {
		// Admins see all tenants.
		rows, err = h.listAll(r, w)
	} else {
		// Regular users see only tenants they're members of.
		rows, err = h.listForUser(r, w, claims.UserID)
	}

	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type tenantDTO struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *TenantHandler) listAll(r *http.Request, w http.ResponseWriter) ([]tenantDTO, error) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, name, slug, created_at FROM tenants WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTenants(rows)
}

func (h *TenantHandler) listForUser(r *http.Request, w http.ResponseWriter, userID uuid.UUID) ([]tenantDTO, error) {
	rows, err := h.pool.Query(r.Context(), `
		SELECT t.id, t.name, t.slug, t.created_at
		FROM   tenants t
		JOIN   tenant_memberships tm ON tm.tenant_id = t.id
		WHERE  tm.user_id = $1 AND t.deleted_at IS NULL
		ORDER  BY t.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTenants(rows)
}

// ── POST /api/tenants ────────────────────────────────────────────

func (h *TenantHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Slug == "" {
		http.Error(w, "name and slug required", http.StatusBadRequest)
		return
	}

	var t tenantDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO tenants (name, slug, origin_node_id)
		VALUES ($1, $2, $3)
		RETURNING id, name, slug, created_at`,
		body.Name, body.Slug, h.selfNode).
		Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedAt)
	if err != nil {
		http.Error(w, "could not create tenant (slug may already exist)", http.StatusConflict)
		return
	}

	writeJSON(w, http.StatusCreated, t)
}

// ── GET /api/tenants/{tenantID} ──────────────────────────────────

func (h *TenantHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	var t tenantDTO
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, name, slug, created_at FROM tenants WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedAt)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, t)
}

// ── DELETE /api/tenants/{tenantID} ──────────────────────────────

func (h *TenantHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`UPDATE tenants SET deleted_at = now(), updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func scanTenants(rows interface{ Next() bool; Scan(...any) error; Err() error }) ([]tenantDTO, error) {
	var result []tenantDTO
	for rows.Next() {
		var t tenantDTO
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}
