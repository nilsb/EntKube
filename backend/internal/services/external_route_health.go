package services

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExternalRouteHealthService HTTP-probes registered external routes and records
// reachability. Runs every 5 minutes with a 10-second per-request timeout.
type ExternalRouteHealthService struct {
	pool   *pgxpool.Pool
	client *http.Client
}

// NewExternalRouteHealthService creates an ExternalRouteHealthService.
func NewExternalRouteHealthService(pool *pgxpool.Pool) *ExternalRouteHealthService {
	return &ExternalRouteHealthService{
		pool: pool,
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Follow up to 5 redirects (default), but don't error.
				return nil
			},
		},
	}
}

// Run probes all routes once.
func (s *ExternalRouteHealthService) Run(ctx context.Context) error {
	type routeRow struct {
		ID         uuid.UUID
		Hostname   string
		PathPrefix string
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, hostname, path_prefix FROM external_routes`)
	if err != nil {
		return fmt.Errorf("query routes: %w", err)
	}
	defer rows.Close()

	var routes []routeRow
	for rows.Next() {
		var r routeRow
		if err := rows.Scan(&r.ID, &r.Hostname, &r.PathPrefix); err != nil {
			return err
		}
		routes = append(routes, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, route := range routes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		s.probe(ctx, route.ID, route.Hostname, route.PathPrefix)
	}
	return nil
}

func (s *ExternalRouteHealthService) probe(ctx context.Context, id uuid.UUID, hostname, pathPrefix string) {
	url := "https://" + hostname + pathPrefix
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		s.record(ctx, id, false, 0, 0)
		return
	}

	resp, err := s.client.Do(req)
	elapsed := int(time.Since(start).Milliseconds())
	if err != nil {
		slog.Debug("route health probe failed", "url", url, "err", err)
		s.record(ctx, id, false, 0, elapsed)
		return
	}
	resp.Body.Close()

	// 2xx–4xx counts as reachable (server responded); 5xx and above = not reachable.
	reachable := resp.StatusCode < 500
	s.record(ctx, id, reachable, resp.StatusCode, elapsed)
}

func (s *ExternalRouteHealthService) record(ctx context.Context, routeID uuid.UUID, reachable bool, statusCode, responseMs int) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		slog.Error("route health record: begin tx", "err", err)
		return
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`UPDATE external_routes
		 SET last_health_check_at = now(),
		     last_status_code     = $2,
		     is_reachable         = $3,
		     updated_at           = now()
		 WHERE id = $1`,
		routeID, nullableInt(statusCode), reachable)
	if err != nil {
		slog.Error("route health update", "route", routeID, "err", err)
		return
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO external_route_health_history (route_id, is_reachable, status_code, response_ms)
		 VALUES ($1, $2, $3, $4)`,
		routeID, reachable, nullableInt(statusCode), responseMs)
	if err != nil {
		slog.Error("route health history insert", "route", routeID, "err", err)
		return
	}

	_ = tx.Commit(ctx)
}

// nullableInt returns nil if v is 0 (no status code), otherwise &v.
func nullableInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
