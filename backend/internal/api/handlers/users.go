package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	authpkg "github.com/entkube/entkube/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserHandler serves /api/users/* (admin-only user management).
type UserHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewUserHandler creates a UserHandler.
func NewUserHandler(pool *pgxpool.Pool, _ *authpkg.Service, selfNode uuid.UUID) *UserHandler {
	return &UserHandler{pool: pool, selfNode: selfNode}
}

type userDTO struct {
	ID        uuid.UUID  `json:"id"`
	Email     string     `json:"email"`
	IsAdmin   bool       `json:"is_admin"`
	CreatedAt time.Time  `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// ── GET /api/users ───────────────────────────────────────────────

func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, email, is_admin, created_at, deleted_at
		 FROM   users
		 WHERE  deleted_at IS NULL
		 ORDER  BY email`)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []userDTO
	for rows.Next() {
		var u userDTO
		if err := rows.Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.DeletedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		users = append(users, u)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// ── POST /api/users ──────────────────────────────────────────────

func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Email == "" || body.Password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}

	hash, err := authpkg.HashPassword(body.Password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var u userDTO
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO users (email, password_hash, is_admin, origin_node_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, email, is_admin, created_at, deleted_at`,
		body.Email, hash, body.IsAdmin, h.selfNode).
		Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.DeletedAt)
	if err != nil {
		http.Error(w, "could not create user (email may already exist)", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

// ── GET /api/users/{userID} ──────────────────────────────────────

func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUserID(r, w)
	if !ok {
		return
	}
	var u userDTO
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, email, is_admin, created_at, deleted_at
		 FROM   users WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.DeletedAt)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ── PATCH /api/users/{userID} ────────────────────────────────────

func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUserID(r, w)
	if !ok {
		return
	}
	var body struct {
		IsAdmin  *bool  `json:"is_admin"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Password != "" {
		hash, err := authpkg.HashPassword(body.Password)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if _, err := h.pool.Exec(r.Context(),
			`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	if body.IsAdmin != nil {
		if _, err := h.pool.Exec(r.Context(),
			`UPDATE users SET is_admin = $2, updated_at = now() WHERE id = $1`, id, *body.IsAdmin); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── DELETE /api/users/{userID} ───────────────────────────────────

func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUserID(r, w)
	if !ok {
		return
	}
	tag, err := h.pool.Exec(r.Context(),
		`UPDATE users SET deleted_at = now(), updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── POST /api/tenants/{tenantID}/members ─────────────────────────

func (h *UserHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	var body struct {
		UserID uuid.UUID `json:"user_id"`
		Role   string    `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID == uuid.Nil {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}
	if body.Role == "" {
		body.Role = "member"
	}
	_, err := h.pool.Exec(r.Context(), `
		INSERT INTO tenant_memberships (user_id, tenant_id, role, origin_node_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, tenant_id) DO UPDATE SET role = EXCLUDED.role, updated_at = now()`,
		body.UserID, tenantID, body.Role, h.selfNode)
	if err != nil {
		http.Error(w, "could not add member", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── DELETE /api/tenants/{tenantID}/members/{userID} ──────────────

func (h *UserHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	id, ok := parseUserID(r, w)
	if !ok {
		return
	}
	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM tenant_memberships WHERE user_id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseUserID(r *http.Request, w http.ResponseWriter) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		http.Error(w, "invalid userID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}
