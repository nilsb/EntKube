package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationHandler serves /api/tenants/{tenantID}/notification-channels/*.
type NotificationHandler struct {
	pool     *pgxpool.Pool
	selfNode uuid.UUID
}

// NewNotificationHandler creates a NotificationHandler.
func NewNotificationHandler(pool *pgxpool.Pool, selfNode uuid.UUID) *NotificationHandler {
	return &NotificationHandler{pool: pool, selfNode: selfNode}
}

type channelDTO struct {
	ID             uuid.UUID `json:"id"`
	TenantID       uuid.UUID `json:"tenant_id"`
	Name           string    `json:"name"`
	ChannelType    string    `json:"channel_type"`
	ConfigJSON     string    `json:"config_json"`
	IsEnabled      bool      `json:"is_enabled"`
	SeverityFilter string    `json:"severity_filter"`
	CreatedAt      time.Time `json:"created_at"`
}

// ── GET /api/tenants/{tenantID}/notification-channels ────────────

func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, tenant_id, name, channel_type::text,
		       configuration_json, is_enabled, severity_filter::text, created_at
		FROM   notification_channels
		WHERE  tenant_id = $1
		ORDER  BY name`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var channels []channelDTO
	for rows.Next() {
		var c channelDTO
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.ChannelType,
			&c.ConfigJSON, &c.IsEnabled, &c.SeverityFilter, &c.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		channels = append(channels, c)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

// ── POST /api/tenants/{tenantID}/notification-channels ───────────

func (h *NotificationHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	var body struct {
		Name           string          `json:"name"`
		ChannelType    string          `json:"channel_type"` // slack | teams | email | webhook
		Config         json.RawMessage `json:"config"`
		SeverityFilter string          `json:"severity_filter"` // all | warning_and_above | critical_only
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Name == "" || body.ChannelType == "" {
		http.Error(w, "name and channel_type required", http.StatusBadRequest)
		return
	}
	if body.SeverityFilter == "" {
		body.SeverityFilter = "all"
	}
	configStr := "{}"
	if len(body.Config) > 0 {
		configStr = string(body.Config)
	}

	var c channelDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO notification_channels
			(tenant_id, name, channel_type, configuration_json, severity_filter, origin_node_id)
		VALUES ($1, $2, $3::notification_channel_type, $4, $5::severity_filter, $6)
		RETURNING id, tenant_id, name, channel_type::text, configuration_json,
		          is_enabled, severity_filter::text, created_at`,
		tenantID, body.Name, body.ChannelType, configStr, body.SeverityFilter, h.selfNode).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.ChannelType,
			&c.ConfigJSON, &c.IsEnabled, &c.SeverityFilter, &c.CreatedAt)
	if err != nil {
		http.Error(w, "could not create channel: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// ── DELETE /api/tenants/{tenantID}/notification-channels/{channelID} ──

func (h *NotificationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	channelID, err := uuid.Parse(r.PathValue("channelID"))
	if err != nil {
		http.Error(w, "invalid channelID", http.StatusBadRequest)
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM notification_channels WHERE id = $1 AND tenant_id = $2`,
		channelID, tenantID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── POST /api/tenants/{tenantID}/notification-channels/{channelID}/test ──

func (h *NotificationHandler) Test(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	channelID, err := uuid.Parse(r.PathValue("channelID"))
	if err != nil {
		http.Error(w, "invalid channelID", http.StatusBadRequest)
		return
	}

	var channelType, configJSON string
	if err := h.pool.QueryRow(r.Context(), `
		SELECT channel_type::text, configuration_json
		FROM   notification_channels
		WHERE  id = $1 AND tenant_id = $2`, channelID, tenantID).
		Scan(&channelType, &configJSON); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if err := sendTestNotification(channelType, configJSON); err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "error", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// sendTestNotification dispatches a test message for the given channel type.
func sendTestNotification(channelType, configJSON string) error {
	var cfg map[string]string
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	switch channelType {
	case "slack", "teams", "webhook":
		webhookURL, ok := cfg["url"]
		if !ok || webhookURL == "" {
			return fmt.Errorf("config missing 'url' field")
		}
		payload, _ := json.Marshal(map[string]string{
			"text": "EntKube test notification — your channel is configured correctly.",
		})
		resp, err := http.Post(webhookURL, "application/json", bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("http post: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("upstream returned %d", resp.StatusCode)
		}
		return nil
	case "email":
		return fmt.Errorf("email notifications not yet implemented")
	default:
		return fmt.Errorf("unknown channel type %q", channelType)
	}
}
