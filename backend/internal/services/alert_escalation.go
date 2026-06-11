package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AlertEscalationService re-notifies on unacknowledged active incidents that
// have exceeded the threshold. Runs every 5 minutes.
type AlertEscalationService struct {
	pool               *pgxpool.Pool
	thresholdMinutes   int
	notifier           IncidentNotifier
}

// IncidentNotifier is implemented by the alerting package's NotificationService.
type IncidentNotifier interface {
	ReNotify(ctx context.Context, incidentID, tenantID uuid.UUID) (channelCount int, err error)
}

// NewAlertEscalationService creates an AlertEscalationService.
func NewAlertEscalationService(pool *pgxpool.Pool, notifier IncidentNotifier, thresholdMinutes int) *AlertEscalationService {
	if thresholdMinutes <= 0 {
		thresholdMinutes = 15
	}
	return &AlertEscalationService{pool: pool, notifier: notifier, thresholdMinutes: thresholdMinutes}
}

// Run escalates all qualifying active incidents.
func (s *AlertEscalationService) Run(ctx context.Context) error {
	cutoff := time.Now().Add(-time.Duration(s.thresholdMinutes) * time.Minute)

	type incident struct {
		ID       uuid.UUID
		TenantID uuid.UUID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, c.tenant_id
		FROM   alert_incidents i
		JOIN   kubernetes_clusters c ON c.id = i.cluster_id
		WHERE  i.status       = 'active'
		  AND  i.escalated_at IS NULL
		  AND  i.starts_at    <= $1
		  AND  i.severity     IN ('critical', 'warning')`,
		cutoff)
	if err != nil {
		return fmt.Errorf("query escalatable incidents: %w", err)
	}
	defer rows.Close()

	var incidents []incident
	for rows.Next() {
		var inc incident
		if err := rows.Scan(&inc.ID, &inc.TenantID); err != nil {
			return err
		}
		incidents = append(incidents, inc)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	escalated := 0
	for _, inc := range incidents {
		count, err := s.notifier.ReNotify(ctx, inc.ID, inc.TenantID)
		if err != nil {
			slog.Error("escalation re-notify failed", "incident", inc.ID, "err", err)
			continue
		}

		// Mark escalated and add an automated note.
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE alert_incidents SET escalated_at = now(), updated_at = now() WHERE id = $1`,
			inc.ID)
		if err != nil {
			tx.Rollback(ctx)
			continue
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO incident_notes (incident_id, author, body)
			 VALUES ($1, 'system', $2)`,
			inc.ID, fmt.Sprintf("Auto-escalated after %d minutes. Re-notified %d channel(s).",
				s.thresholdMinutes, count))
		if err != nil {
			tx.Rollback(ctx)
			continue
		}
		if err := tx.Commit(ctx); err != nil {
			slog.Error("commit escalation", "incident", inc.ID, "err", err)
			continue
		}
		escalated++
	}

	if escalated > 0 {
		slog.Info("alert escalation pass complete", "escalated", escalated)
	}
	return nil
}
