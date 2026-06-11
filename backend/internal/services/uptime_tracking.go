package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const uptimeRetentionDays = 90

// UptimeTrackingService snapshots deployment health into deployment_health_snapshots
// and prunes records older than 90 days. Runs every 5 minutes.
type UptimeTrackingService struct {
	pool *pgxpool.Pool
}

// NewUptimeTrackingService creates a UptimeTrackingService.
func NewUptimeTrackingService(pool *pgxpool.Pool) *UptimeTrackingService {
	return &UptimeTrackingService{pool: pool}
}

// Run records one snapshot per deployment and prunes stale data.
func (s *UptimeTrackingService) Run(ctx context.Context) error {
	snapped, err := s.snapshotAll(ctx)
	if err != nil {
		return fmt.Errorf("snapshot deployments: %w", err)
	}

	prunedSnaps, prunedRoutes, err := s.pruneOld(ctx)
	if err != nil {
		return fmt.Errorf("prune old data: %w", err)
	}

	slog.Info("uptime tracking pass complete",
		"snapshots_recorded", snapped,
		"snapshots_pruned", prunedSnaps,
		"route_history_pruned", prunedRoutes,
	)
	return nil
}

func (s *UptimeTrackingService) snapshotAll(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO deployment_health_snapshots (deployment_id, health_status, sync_status, snapshot_at)
		SELECT id, health_status, sync_status, now()
		FROM   app_deployments`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *UptimeTrackingService) pruneOld(ctx context.Context) (snaps, routes int64, err error) {
	cutoff := time.Now().AddDate(0, 0, -uptimeRetentionDays)

	tag, err := s.pool.Exec(ctx,
		`DELETE FROM deployment_health_snapshots WHERE snapshot_at < $1`, cutoff)
	if err != nil {
		return 0, 0, err
	}
	snaps = tag.RowsAffected()

	tag, err = s.pool.Exec(ctx,
		`DELETE FROM external_route_health_history WHERE checked_at < $1`, cutoff)
	if err != nil {
		return snaps, 0, err
	}
	return snaps, tag.RowsAffected(), nil
}
