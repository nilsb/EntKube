package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	authpkg "github.com/entkube/entkube/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AlertHandler serves /api/tenants/{tenantID}/alerts/*.
type AlertHandler struct {
	pool *pgxpool.Pool
}

// NewAlertHandler creates an AlertHandler.
func NewAlertHandler(pool *pgxpool.Pool) *AlertHandler {
	return &AlertHandler{pool: pool}
}

type incidentDTO struct {
	ID             uuid.UUID  `json:"id"`
	ClusterID      uuid.UUID  `json:"cluster_id"`
	ClusterName    string     `json:"cluster_name"`
	Fingerprint    string     `json:"fingerprint"`
	AlertName      string     `json:"alert_name"`
	Severity       string     `json:"severity"`
	Summary        string     `json:"summary"`
	Description    string     `json:"description"`
	RunbookURL     string     `json:"runbook_url"`
	LabelsJSON     string     `json:"labels_json"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         *time.Time `json:"ends_at,omitempty"`
	Status         string     `json:"status"`
	AcknowledgedBy *string    `json:"acknowledged_by,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	EscalatedAt    *time.Time `json:"escalated_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ── GET /api/tenants/{tenantID}/alerts ──────────────────────────

func (h *AlertHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	status := r.URL.Query().Get("status") // optional filter: active, acknowledged, resolved
	severity := r.URL.Query().Get("severity")

	query := `
		SELECT ai.id, ai.cluster_id, kc.name,
		       ai.fingerprint, ai.alert_name, ai.severity,
		       ai.summary, ai.description, ai.runbook_url, ai.labels_json,
		       ai.starts_at, ai.ends_at, ai.status,
		       ai.acknowledged_by, ai.acknowledged_at,
		       ai.resolved_at, ai.escalated_at,
		       ai.created_at, ai.updated_at
		FROM   alert_incidents ai
		JOIN   kubernetes_clusters kc ON kc.id = ai.cluster_id
		WHERE  kc.tenant_id = $1`
	args := []any{tenantID}

	if status != "" {
		args = append(args, status)
		query += " AND ai.status = $2"
	}
	if severity != "" && len(args) == 1 {
		args = append(args, severity)
		query += " AND ai.severity = $2"
	} else if severity != "" {
		args = append(args, severity)
		query += " AND ai.severity = $3"
	}
	query += " ORDER BY ai.starts_at DESC LIMIT 500"

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var incidents []incidentDTO
	for rows.Next() {
		var inc incidentDTO
		if err := rows.Scan(
			&inc.ID, &inc.ClusterID, &inc.ClusterName,
			&inc.Fingerprint, &inc.AlertName, &inc.Severity,
			&inc.Summary, &inc.Description, &inc.RunbookURL, &inc.LabelsJSON,
			&inc.StartsAt, &inc.EndsAt, &inc.Status,
			&inc.AcknowledgedBy, &inc.AcknowledgedAt,
			&inc.ResolvedAt, &inc.EscalatedAt,
			&inc.CreatedAt, &inc.UpdatedAt,
		); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		incidents = append(incidents, inc)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, incidents)
}

// ── POST /api/tenants/{tenantID}/alerts/{alertID}/acknowledge ────

func (h *AlertHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	alertID, ok := parseAlertID(r, w)
	if !ok {
		return
	}
	claims := authpkg.ClaimsFromCtx(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Ensure this incident belongs to a cluster in the tenant.
	tag, err := h.pool.Exec(r.Context(), `
		UPDATE alert_incidents ai
		SET    status           = 'acknowledged',
		       acknowledged_by  = $3,
		       acknowledged_at  = now(),
		       updated_at       = now()
		FROM   kubernetes_clusters kc
		WHERE  kc.id = ai.cluster_id
		  AND  kc.tenant_id = $2
		  AND  ai.id = $1
		  AND  ai.status = 'active'`,
		alertID, tenantID, claims.UserID.String())
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "incident not found or not active", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── POST /api/tenants/{tenantID}/alerts/{alertID}/resolve ────────

func (h *AlertHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	alertID, ok := parseAlertID(r, w)
	if !ok {
		return
	}

	tag, err := h.pool.Exec(r.Context(), `
		UPDATE alert_incidents ai
		SET    status      = 'resolved',
		       resolved_at = now(),
		       updated_at  = now()
		FROM   kubernetes_clusters kc
		WHERE  kc.id = ai.cluster_id
		  AND  kc.tenant_id = $2
		  AND  ai.id = $1
		  AND  ai.status != 'resolved'`,
		alertID, tenantID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "incident not found or already resolved", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── GET /api/tenants/{tenantID}/alerts/{alertID}/notes ───────────

func (h *AlertHandler) ListNotes(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	alertID, ok := parseAlertID(r, w)
	if !ok {
		return
	}

	// Verify ownership.
	if !h.incidentInTenant(r, alertID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	type noteDTO struct {
		ID         uuid.UUID `json:"id"`
		IncidentID uuid.UUID `json:"incident_id"`
		Author     string    `json:"author"`
		Body       string    `json:"body"`
		CreatedAt  time.Time `json:"created_at"`
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT id, incident_id, author, body, created_at
		 FROM   incident_notes
		 WHERE  incident_id = $1
		 ORDER  BY created_at ASC`, alertID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var notes []noteDTO
	for rows.Next() {
		var n noteDTO
		if err := rows.Scan(&n.ID, &n.IncidentID, &n.Author, &n.Body, &n.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		notes = append(notes, n)
	}
	writeJSON(w, http.StatusOK, notes)
}

// ── POST /api/tenants/{tenantID}/alerts/{alertID}/notes ──────────

func (h *AlertHandler) AddNote(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	alertID, ok := parseAlertID(r, w)
	if !ok {
		return
	}
	claims := authpkg.ClaimsFromCtx(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.incidentInTenant(r, alertID, tenantID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Body == "" {
		http.Error(w, "body required", http.StatusBadRequest)
		return
	}

	var noteID uuid.UUID
	var createdAt time.Time
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO incident_notes (incident_id, author, body)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`,
		alertID, claims.UserID.String(), body.Body).Scan(&noteID, &createdAt)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          noteID,
		"incident_id": alertID,
		"author":      claims.UserID.String(),
		"body":        body.Body,
		"created_at":  createdAt,
	})
}

func (h *AlertHandler) incidentInTenant(r *http.Request, incidentID, tenantID uuid.UUID) bool {
	var exists bool
	_ = h.pool.QueryRow(r.Context(), `
		SELECT EXISTS(
			SELECT 1 FROM alert_incidents ai
			JOIN   kubernetes_clusters kc ON kc.id = ai.cluster_id
			WHERE  ai.id = $1 AND kc.tenant_id = $2)`,
		incidentID, tenantID).Scan(&exists)
	return exists
}

func parseAlertID(r *http.Request, w http.ResponseWriter) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("alertID"))
	if err != nil {
		http.Error(w, "invalid alertID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}
