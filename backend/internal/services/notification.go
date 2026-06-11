package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationService implements IncidentNotifier by dispatching to all
// enabled notification channels for a tenant. Supports slack, teams, and
// generic webhook channels (all share the same incoming-webhook protocol).
type NotificationService struct {
	pool   *pgxpool.Pool
	client *http.Client
}

// NewNotificationService creates a NotificationService.
func NewNotificationService(pool *pgxpool.Pool) *NotificationService {
	return &NotificationService{
		pool:   pool,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// ReNotify dispatches the incident to all enabled channels for the tenant.
// Returns the number of channels successfully notified.
func (s *NotificationService) ReNotify(ctx context.Context, incidentID, tenantID uuid.UUID) (int, error) {
	// Load the incident details.
	type incident struct {
		AlertName string
		Severity  string
		Summary   string
		ClusterID uuid.UUID
		StartsAt  time.Time
	}
	var inc incident
	err := s.pool.QueryRow(ctx, `
		SELECT alert_name, severity, summary, cluster_id, starts_at
		FROM   alert_incidents
		WHERE  id = $1`, incidentID).
		Scan(&inc.AlertName, &inc.Severity, &inc.Summary, &inc.ClusterID, &inc.StartsAt)
	if err != nil {
		return 0, fmt.Errorf("load incident: %w", err)
	}

	// Load enabled channels for this tenant, filtered by severity.
	type channel struct {
		ID          uuid.UUID
		Name        string
		ChannelType string
		ConfigJSON  string
		Filter      string
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, channel_type::text, configuration_json, severity_filter::text
		FROM   notification_channels
		WHERE  tenant_id = $1 AND is_enabled = TRUE`, tenantID)
	if err != nil {
		return 0, fmt.Errorf("load channels: %w", err)
	}
	defer rows.Close()

	var channels []channel
	for rows.Next() {
		var ch channel
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.ChannelType, &ch.ConfigJSON, &ch.Filter); err != nil {
			return 0, err
		}
		channels = append(channels, ch)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	sent := 0
	for _, ch := range channels {
		// Apply severity filter.
		if !severityAllowed(inc.Severity, ch.Filter) {
			continue
		}

		var sendErr error
		switch ch.ChannelType {
		case "slack", "teams", "webhook":
			sendErr = s.sendWebhook(ctx, ch.ID, ch.ConfigJSON, inc.AlertName, inc.Severity, inc.Summary, incidentID)
		default:
			slog.Warn("unsupported channel type for notification", "type", ch.ChannelType)
			continue
		}

		success := sendErr == nil
		errMsg := ""
		if sendErr != nil {
			errMsg = sendErr.Error()
			slog.Error("notification delivery failed", "channel", ch.ID, "err", sendErr)
		} else {
			sent++
		}

		// Record delivery attempt.
		s.pool.Exec(ctx, `
			INSERT INTO notification_deliveries (incident_id, channel_id, success, error_msg)
			VALUES ($1, $2, $3, $4)`,
			incidentID, ch.ID, success, nullableStrVal(errMsg))
	}

	return sent, nil
}

func (s *NotificationService) sendWebhook(
	ctx context.Context,
	channelID uuid.UUID,
	configJSON, alertName, severity, summary string,
	incidentID uuid.UUID,
) error {
	var cfg map[string]string
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	webhookURL := cfg["url"]
	if webhookURL == "" {
		return fmt.Errorf("channel config missing 'url'")
	}

	payload, _ := json.Marshal(map[string]string{
		"text": fmt.Sprintf("[EntKube] *%s* (%s)\n%s\nIncident: %s",
			alertName, severity, summary, incidentID),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// severityAllowed returns true if the incident severity passes the channel filter.
func severityAllowed(severity, filter string) bool {
	switch filter {
	case "critical_only":
		return severity == "critical"
	case "warning_and_above":
		return severity == "critical" || severity == "warning"
	default: // "all"
		return true
	}
}

func nullableStrVal(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
