package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AlertRuleHandler serves /api/tenants/{tenantID}/alert-rules/*.
type AlertRuleHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewAlertRuleHandler creates an AlertRuleHandler.
func NewAlertRuleHandler(pool *pgxpool.Pool, selfNode uuid.UUID) *AlertRuleHandler {
	return &AlertRuleHandler{pool: pool, selfNode: selfNode}
}

type alertRuleDTO struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	Name             string     `json:"name"`
	Priority         int        `json:"priority"`
	IsEnabled        bool       `json:"is_enabled"`
	SuppressIncident bool       `json:"suppress_incident"`
	MatchAlertName   *string    `json:"match_alert_name,omitempty"`
	MatchNamespace   *string    `json:"match_namespace,omitempty"`
	MatchSeverity    *string    `json:"match_severity,omitempty"`
	MatchLabelKey    *string    `json:"match_label_key,omitempty"`
	MatchLabelValue  *string    `json:"match_label_value,omitempty"`
	MatchClusterID   *uuid.UUID `json:"match_cluster_id,omitempty"`
	ChannelID        *uuid.UUID `json:"channel_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ── GET /api/tenants/{tenantID}/alert-rules ──────────────────────

func (h *AlertRuleHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, tenant_id, name, priority, is_enabled, suppress_incident,
		       match_alert_name, match_namespace, match_severity,
		       match_label_key, match_label_value, match_cluster_id,
		       channel_id, created_at, updated_at
		FROM   alert_routing_rules
		WHERE  tenant_id = $1
		ORDER  BY priority ASC`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var rules []alertRuleDTO
	for rows.Next() {
		var rule alertRuleDTO
		if err := rows.Scan(
			&rule.ID, &rule.TenantID, &rule.Name, &rule.Priority,
			&rule.IsEnabled, &rule.SuppressIncident,
			&rule.MatchAlertName, &rule.MatchNamespace, &rule.MatchSeverity,
			&rule.MatchLabelKey, &rule.MatchLabelValue, &rule.MatchClusterID,
			&rule.ChannelID, &rule.CreatedAt, &rule.UpdatedAt,
		); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rules = append(rules, rule)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

// ── POST /api/tenants/{tenantID}/alert-rules ─────────────────────

func (h *AlertRuleHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	var body struct {
		Name             string     `json:"name"`
		Priority         int        `json:"priority"`
		SuppressIncident bool       `json:"suppress_incident"`
		MatchAlertName   *string    `json:"match_alert_name"`
		MatchNamespace   *string    `json:"match_namespace"`
		MatchSeverity    *string    `json:"match_severity"`
		MatchLabelKey    *string    `json:"match_label_key"`
		MatchLabelValue  *string    `json:"match_label_value"`
		MatchClusterID   *uuid.UUID `json:"match_cluster_id"`
		ChannelID        *uuid.UUID `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if body.Priority == 0 {
		body.Priority = 100
	}

	var rule alertRuleDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO alert_routing_rules
			(tenant_id, name, priority, suppress_incident,
			 match_alert_name, match_namespace, match_severity,
			 match_label_key, match_label_value, match_cluster_id,
			 channel_id, origin_node_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, tenant_id, name, priority, is_enabled, suppress_incident,
		          match_alert_name, match_namespace, match_severity,
		          match_label_key, match_label_value, match_cluster_id,
		          channel_id, created_at, updated_at`,
		tenantID, body.Name, body.Priority, body.SuppressIncident,
		body.MatchAlertName, body.MatchNamespace, body.MatchSeverity,
		body.MatchLabelKey, body.MatchLabelValue, body.MatchClusterID,
		body.ChannelID, h.selfNode).
		Scan(&rule.ID, &rule.TenantID, &rule.Name, &rule.Priority,
			&rule.IsEnabled, &rule.SuppressIncident,
			&rule.MatchAlertName, &rule.MatchNamespace, &rule.MatchSeverity,
			&rule.MatchLabelKey, &rule.MatchLabelValue, &rule.MatchClusterID,
			&rule.ChannelID, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		http.Error(w, "could not create rule: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// ── PATCH /api/tenants/{tenantID}/alert-rules/{ruleID} ───────────

func (h *AlertRuleHandler) ToggleEnabled(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	ruleID, err := uuid.Parse(r.PathValue("ruleID"))
	if err != nil {
		http.Error(w, "invalid ruleID", http.StatusBadRequest)
		return
	}

	var body struct {
		IsEnabled bool `json:"is_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	tag, err := h.pool.Exec(r.Context(), `
		UPDATE alert_routing_rules
		SET    is_enabled = $3, updated_at = now()
		WHERE  id = $1 AND tenant_id = $2`, ruleID, tenantID, body.IsEnabled)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── DELETE /api/tenants/{tenantID}/alert-rules/{ruleID} ──────────

func (h *AlertRuleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	ruleID, err := uuid.Parse(r.PathValue("ruleID"))
	if err != nil {
		http.Error(w, "invalid ruleID", http.StatusBadRequest)
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM alert_routing_rules WHERE id = $1 AND tenant_id = $2`,
		ruleID, tenantID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
