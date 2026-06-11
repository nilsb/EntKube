package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CustomerHandler serves /api/tenants/{tenantID}/customers/*.
type CustomerHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewCustomerHandler creates a CustomerHandler.
func NewCustomerHandler(pool *pgxpool.Pool, selfNode uuid.UUID) *CustomerHandler {
	return &CustomerHandler{pool: pool, selfNode: selfNode}
}

type customerDTO struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// ── GET /api/tenants/{tenantID}/customers ────────────────────────

func (h *CustomerHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT id, tenant_id, name, created_at
		 FROM   customers
		 WHERE  tenant_id = $1
		 ORDER  BY name`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var customers []customerDTO
	for rows.Next() {
		var c customerDTO
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		customers = append(customers, c)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, customers)
}

// ── POST /api/tenants/{tenantID}/customers ───────────────────────

func (h *CustomerHandler) Create(w http.ResponseWriter, r *http.Request) {
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

	var c customerDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO customers (tenant_id, name, origin_node_id)
		VALUES ($1, $2, $3)
		RETURNING id, tenant_id, name, created_at`,
		tenantID, body.Name, h.selfNode).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.CreatedAt)
	if err != nil {
		http.Error(w, "could not create customer (name may already exist)", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// ── GET /api/tenants/{tenantID}/customers/{customerID} ───────────

func (h *CustomerHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	customerID, ok := parseCustomerID(r, w)
	if !ok {
		return
	}

	var c customerDTO
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, tenant_id, name, created_at
		 FROM   customers WHERE id = $1 AND tenant_id = $2`,
		customerID, tenantID).Scan(&c.ID, &c.TenantID, &c.Name, &c.CreatedAt)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ── DELETE /api/tenants/{tenantID}/customers/{customerID} ────────

func (h *CustomerHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	customerID, ok := parseCustomerID(r, w)
	if !ok {
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM customers WHERE id = $1 AND tenant_id = $2`, customerID, tenantID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseCustomerID(r *http.Request, w http.ResponseWriter) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("customerID"))
	if err != nil {
		http.Error(w, "invalid customerID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}
