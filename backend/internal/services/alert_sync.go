package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/entkube/entkube/internal/alerting"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AlertSyncService polls Alertmanager on each cluster that has Prometheus
// installed and reconciles alert_incidents rows. Runs every 2 minutes.
type AlertSyncService struct {
	pool *pgxpool.Pool
}

// NewAlertSyncService creates an AlertSyncService.
func NewAlertSyncService(pool *pgxpool.Pool) *AlertSyncService {
	return &AlertSyncService{pool: pool}
}

// Run syncs alerts for all clusters with an installed Prometheus component.
func (s *AlertSyncService) Run(ctx context.Context) error {
	type clusterRow struct {
		ClusterID       uuid.UUID
		TenantID        uuid.UUID
		AlertmanagerURL string
	}

	// Find clusters with kube-prometheus-stack installed via a component whose
	// helm_chart_name = 'kube-prometheus-stack' and status = 'installed'.
	// The Alertmanager URL is derived from the component's release_name.
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.tenant_id,
		       'http://' || COALESCE(cc.release_name, cc.name) || '-alertmanager.' || COALESCE(cc.namespace, 'monitoring') || '.svc.cluster.local:9093' AS alertmanager_url
		FROM   cluster_components cc
		JOIN   kubernetes_clusters c ON c.id = cc.cluster_id
		WHERE  cc.helm_chart_name = 'kube-prometheus-stack'
		  AND  cc.status         = 'installed'`)
	if err != nil {
		return fmt.Errorf("query prometheus clusters: %w", err)
	}
	defer rows.Close()

	var clusters []clusterRow
	for rows.Next() {
		var r clusterRow
		if err := rows.Scan(&r.ClusterID, &r.TenantID, &r.AlertmanagerURL); err != nil {
			return err
		}
		clusters = append(clusters, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, cluster := range clusters {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := s.syncCluster(ctx, cluster.ClusterID, cluster.TenantID, cluster.AlertmanagerURL); err != nil {
			slog.Error("alert sync failed for cluster", "cluster", cluster.ClusterID, "err", err)
		}
	}
	return nil
}

func (s *AlertSyncService) syncCluster(ctx context.Context, clusterID, tenantID uuid.UUID, alertmanagerURL string) error {
	amClient := alerting.NewClient(alertmanagerURL)
	liveAlerts, err := amClient.GetAlerts(ctx)
	if err != nil {
		return fmt.Errorf("fetch alerts: %w", err)
	}

	// Build a fingerprint → alert map for O(1) lookup.
	liveByFP := make(map[string]alerting.Alert, len(liveAlerts))
	for _, a := range liveAlerts {
		liveByFP[a.Fingerprint] = a
	}

	// Load all existing incidents for this cluster (including Resolved, to
	// prevent duplicate fingerprint rows).
	type incidentRow struct {
		ID          uuid.UUID
		Fingerprint string
		Status      string
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, fingerprint, status FROM alert_incidents WHERE cluster_id = $1`, clusterID)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]incidentRow)
	for rows.Next() {
		var inc incidentRow
		if err := rows.Scan(&inc.ID, &inc.Fingerprint, &inc.Status); err != nil {
			return err
		}
		existing[inc.Fingerprint] = inc
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Load active routing rules for this tenant (ordered by priority).
	ruleRows, err := s.pool.Query(ctx, `
		SELECT suppress_incident, match_alert_name, match_namespace, match_severity, match_cluster_id
		FROM   alert_routing_rules
		WHERE  tenant_id = $1 AND is_enabled = TRUE
		ORDER  BY priority ASC`, tenantID)
	if err != nil {
		return err
	}
	defer ruleRows.Close()
	var rules []alertRoutingRule
	for ruleRows.Next() {
		var r alertRoutingRule
		if err := ruleRows.Scan(&r.SuppressIncident, &r.MatchAlertName, &r.MatchNamespace, &r.MatchSeverity, &r.MatchClusterID); err != nil {
			return err
		}
		rules = append(rules, r)
	}

	// Check active maintenance windows.
	var inMaintenance bool
	_ = s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM maintenance_windows
			WHERE tenant_id = $1 AND starts_at <= now() AND ends_at >= now()
		)`, tenantID).Scan(&inMaintenance)

	// Reconcile: create/reactivate/resolve incidents.
	for fp, alert := range liveByFP {
		inc, exists := existing[fp]
		suppressed := s.isSuppressed(alert, clusterID, rules)

		if !exists {
			if suppressed {
				continue
			}
			s.createIncident(ctx, clusterID, alert)
		} else if inc.Status == "resolved" && !suppressed {
			// Re-fire: reactivate the existing row so notes are preserved.
			s.reactivateIncident(ctx, inc.ID, alert)
		} else {
			// Still active — update mutable fields that may have changed.
			s.updateIncident(ctx, inc.ID, alert)
		}
	}

	// Mark incidents that are no longer in Alertmanager as resolved.
	for fp, inc := range existing {
		if inc.Status != "active" {
			continue
		}
		if _, stillLive := liveByFP[fp]; !stillLive {
			s.resolveIncident(ctx, inc.ID)
		}
	}

	return nil
}

type alertRoutingRule struct {
	SuppressIncident bool
	MatchAlertName   *string
	MatchNamespace   *string
	MatchSeverity    *string
	MatchClusterID   *uuid.UUID
}

func (s *AlertSyncService) isSuppressed(alert alerting.Alert, clusterID uuid.UUID, rules []alertRoutingRule) bool {
	alertName := alert.Labels["alertname"]
	namespace := alert.Labels["namespace"]
	severity := alert.AlertSeverity()

	for _, r := range rules {
		if r.MatchClusterID != nil && *r.MatchClusterID != clusterID {
			continue
		}
		if r.MatchAlertName != nil && !strings.Contains(alertName, *r.MatchAlertName) {
			continue
		}
		if r.MatchNamespace != nil && namespace != *r.MatchNamespace {
			continue
		}
		if r.MatchSeverity != nil && severity != *r.MatchSeverity {
			continue
		}
		return r.SuppressIncident
	}
	return false
}

func (s *AlertSyncService) createIncident(ctx context.Context, clusterID uuid.UUID, a alerting.Alert) {
	labelsJSON, _ := json.Marshal(a.Labels)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO alert_incidents
			(cluster_id, fingerprint, alert_name, severity, summary, description, runbook_url, labels_json, starts_at, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'active')
		ON CONFLICT (cluster_id, fingerprint) DO NOTHING`,
		clusterID, a.Fingerprint, a.Labels["alertname"], a.AlertSeverity(),
		a.Summary(), a.Description(), a.RunbookURL(), string(labelsJSON), a.StartsAt)
	if err != nil {
		slog.Error("create incident", "fp", a.Fingerprint, "err", err)
	}
}

func (s *AlertSyncService) reactivateIncident(ctx context.Context, id uuid.UUID, a alerting.Alert) {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_incidents
		SET status = 'active', resolved_at = NULL, escalated_at = NULL,
		    starts_at = $2, ends_at = NULL, updated_at = now()
		WHERE id = $1`, id, a.StartsAt)
	if err != nil {
		slog.Error("reactivate incident", "id", id, "err", err)
	}
}

func (s *AlertSyncService) updateIncident(ctx context.Context, id uuid.UUID, a alerting.Alert) {
	var endsAt *time.Time
	if !a.EndsAt.IsZero() {
		endsAt = &a.EndsAt
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_incidents
		SET summary = $2, description = $3, ends_at = $4, updated_at = now()
		WHERE id = $1`, id, a.Summary(), a.Description(), endsAt)
	if err != nil {
		slog.Error("update incident", "id", id, "err", err)
	}
}

func (s *AlertSyncService) resolveIncident(ctx context.Context, id uuid.UUID) {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_incidents
		SET status = 'resolved', resolved_at = now(), updated_at = now()
		WHERE id = $1`, id)
	if err != nil {
		slog.Error("resolve incident", "id", id, "err", err)
	}
}
