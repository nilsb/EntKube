package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AppHandler serves /api/tenants/{tenantID}/customers/{customerID}/apps/*.
type AppHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewAppHandler creates an AppHandler.
func NewAppHandler(pool *pgxpool.Pool, selfNode uuid.UUID) *AppHandler {
	return &AppHandler{pool: pool, selfNode: selfNode}
}

type appDTO struct {
	ID         uuid.UUID  `json:"id"`
	CustomerID uuid.UUID  `json:"customer_id"`
	Name       string     `json:"name"`
	Namespace  *string    `json:"namespace,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ── GET /api/tenants/{tenantID}/customers/{customerID}/apps ──────

func (h *AppHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	customerID, ok := parseCustomerID(r, w)
	if !ok {
		return
	}

	// Verify customer belongs to tenant.
	if !h.customerInTenant(r, customerID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT id, customer_id, name, namespace, created_at
		 FROM   apps
		 WHERE  customer_id = $1
		 ORDER  BY name`, customerID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var apps []appDTO
	for rows.Next() {
		var a appDTO
		if err := rows.Scan(&a.ID, &a.CustomerID, &a.Name, &a.Namespace, &a.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		apps = append(apps, a)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

// ── POST /api/tenants/{tenantID}/customers/{customerID}/apps ─────

func (h *AppHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	customerID, ok := parseCustomerID(r, w)
	if !ok {
		return
	}
	if !h.customerInTenant(r, customerID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var body struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	var a appDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO apps (customer_id, name, namespace, origin_node_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, customer_id, name, namespace, created_at`,
		customerID, body.Name,
		nullableStr(body.Namespace, body.Namespace != ""),
		h.selfNode).
		Scan(&a.ID, &a.CustomerID, &a.Name, &a.Namespace, &a.CreatedAt)
	if err != nil {
		http.Error(w, "could not create app (name may already exist)", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// ── DELETE /api/tenants/{tenantID}/customers/{customerID}/apps/{appID} ──

func (h *AppHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	customerID, ok := parseCustomerID(r, w)
	if !ok {
		return
	}
	appID, ok := parseAppID(r, w)
	if !ok {
		return
	}
	if !h.customerInTenant(r, customerID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM apps WHERE id = $1 AND customer_id = $2`, appID, customerID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AppHandler) customerInTenant(r *http.Request, customerID, tenantID uuid.UUID) bool {
	var exists bool
	_ = h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM customers WHERE id = $1 AND tenant_id = $2)`,
		customerID, tenantID).Scan(&exists)
	return exists
}

func parseAppID(r *http.Request, w http.ResponseWriter) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("appID"))
	if err != nil {
		http.Error(w, "invalid appID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}
