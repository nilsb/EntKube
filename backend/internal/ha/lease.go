package ha

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	leaseDuration = 30 * time.Second
	leaseRenewAt  = 15 * time.Second // renew when half the lease is left
)

// LeaseManager acquires and holds named service leases so that exactly one
// node in the cluster runs each background service at a time.
type LeaseManager struct {
	pool   *pgxpool.Pool
	nodeID uuid.UUID
}

// NewLeaseManager creates a LeaseManager for the given node.
func NewLeaseManager(pool *pgxpool.Pool, nodeID uuid.UUID) *LeaseManager {
	return &LeaseManager{pool: pool, nodeID: nodeID}
}

// RunWithLease acquires the named lease and calls fn while holding it,
// renewing in the background. fn should respect ctx cancellation.
// Returns immediately if the lease cannot be acquired (another node holds it).
func (lm *LeaseManager) RunWithLease(ctx context.Context, service string, fn func(ctx context.Context)) {
	if !lm.tryAcquire(ctx, service) {
		return
	}

	slog.Info("acquired lease", "service", service, "node", lm.nodeID)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go lm.renewLoop(ctx, service, cancel)
	fn(ctx)
}

// tryAcquire attempts to insert/take the lease row atomically.
func (lm *LeaseManager) tryAcquire(ctx context.Context, service string) bool {
	expiry := time.Now().Add(leaseDuration)

	// Use INSERT ... ON CONFLICT to claim the lease if it's expired or absent.
	tag, err := lm.pool.Exec(ctx, `
		INSERT INTO ha_leases (service_name, node_id, expires_at, renewed_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (service_name) DO UPDATE
			SET node_id    = EXCLUDED.node_id,
			    expires_at = EXCLUDED.expires_at,
			    renewed_at = now()
			WHERE ha_leases.expires_at < now()`,
		service, lm.nodeID, expiry)
	if err != nil {
		slog.Error("lease acquire error", "service", service, "err", err)
		return false
	}
	return tag.RowsAffected() == 1
}

// renewLoop periodically refreshes the lease expiry. Cancels ctx if renewal fails
// (meaning another node stole the lease or the DB is unavailable).
func (lm *LeaseManager) renewLoop(ctx context.Context, service string, cancel context.CancelFunc) {
	ticker := time.NewTicker(leaseRenewAt)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			lm.release(service)
			return
		case <-ticker.C:
			if !lm.renew(ctx, service) {
				slog.Warn("lost lease, stopping service", "service", service, "node", lm.nodeID)
				cancel()
				return
			}
		}
	}
}

func (lm *LeaseManager) renew(ctx context.Context, service string) bool {
	expiry := time.Now().Add(leaseDuration)
	tag, err := lm.pool.Exec(ctx, `
		UPDATE ha_leases
		   SET expires_at = $1, renewed_at = now()
		 WHERE service_name = $2 AND node_id = $3`,
		expiry, service, lm.nodeID)
	if err != nil {
		slog.Error("lease renew error", "service", service, "err", err)
		return false
	}
	return tag.RowsAffected() == 1
}

func (lm *LeaseManager) release(service string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = lm.pool.Exec(ctx,
		`DELETE FROM ha_leases WHERE service_name = $1 AND node_id = $2`,
		service, lm.nodeID)
	slog.Info("released lease", "service", service, "node", lm.nodeID)
}

// RunServiceLoop is a helper that calls fn on a ticker interval, wrapped in
// lease acquisition, restarting acquisition attempts when the lease is absent.
func (lm *LeaseManager) RunServiceLoop(
	ctx context.Context,
	service string,
	interval time.Duration,
	fn func(ctx context.Context) error,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		lm.RunWithLease(ctx, service, func(ctx context.Context) {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := fn(ctx); err != nil {
						slog.Error("service error", "service", service, "err", err)
					}
				}
			}
		})

		// Back off before retrying lease acquisition.
		select {
		case <-ctx.Done():
			return
		case <-time.After(leaseDuration):
		}
	}
}

// IsLeader reports whether this node currently holds the named lease.
// Intended for informational/health-check use only.
func (lm *LeaseManager) IsLeader(ctx context.Context, service string) (bool, error) {
	var nodeID uuid.UUID
	err := lm.pool.QueryRow(ctx,
		`SELECT node_id FROM ha_leases WHERE service_name = $1 AND expires_at > now()`,
		service).Scan(&nodeID)
	if err != nil {
		return false, fmt.Errorf("check lease: %w", err)
	}
	return nodeID == lm.nodeID, nil
}
