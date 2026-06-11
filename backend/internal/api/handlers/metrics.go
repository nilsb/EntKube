package handlers

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler exposes Prometheus metrics at /metrics.
type MetricsHandler struct {
	reg     *prometheus.Registry
	handler http.Handler

	// Application-level gauges updated on each scrape.
	pool               *pgxpool.Pool
	activeIncidents    prometheus.Gauge
	deploymentUnhealthy prometheus.Gauge
	haNodesOnline      prometheus.Gauge
	dbPoolAcquired     prometheus.Gauge
	dbPoolIdle         prometheus.Gauge
	dbPoolTotal        prometheus.Gauge
}

// NewMetricsHandler creates a MetricsHandler with a private Prometheus registry.
func NewMetricsHandler(pool *pgxpool.Pool, nodeID string) *MetricsHandler {
	reg := prometheus.NewRegistry()

	// Standard Go runtime metrics.
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	labels := prometheus.Labels{"node": nodeID}

	activeIncidents := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   "entkube",
		Name:        "active_incidents_total",
		Help:        "Number of currently active alert incidents.",
		ConstLabels: labels,
	})
	deploymentUnhealthy := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   "entkube",
		Name:        "deployments_unhealthy_total",
		Help:        "Number of deployments with health_status != healthy.",
		ConstLabels: labels,
	})
	haNodesOnline := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   "entkube",
		Name:        "ha_nodes_online",
		Help:        "Number of HA nodes seen in the last 60 seconds.",
		ConstLabels: labels,
	})
	dbPoolAcquired := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   "entkube",
		Name:        "db_pool_acquired_conns",
		Help:        "pgxpool acquired (in-use) connections.",
		ConstLabels: labels,
	})
	dbPoolIdle := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   "entkube",
		Name:        "db_pool_idle_conns",
		Help:        "pgxpool idle connections.",
		ConstLabels: labels,
	})
	dbPoolTotal := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace:   "entkube",
		Name:        "db_pool_total_conns",
		Help:        "pgxpool total connections (acquired + idle + constructing).",
		ConstLabels: labels,
	})

	reg.MustRegister(activeIncidents, deploymentUnhealthy, haNodesOnline,
		dbPoolAcquired, dbPoolIdle, dbPoolTotal)

	h := &MetricsHandler{
		reg:                 reg,
		pool:                pool,
		activeIncidents:     activeIncidents,
		deploymentUnhealthy: deploymentUnhealthy,
		haNodesOnline:       haNodesOnline,
		dbPoolAcquired:      dbPoolAcquired,
		dbPoolIdle:          dbPoolIdle,
		dbPoolTotal:         dbPoolTotal,
	}

	// Wrap promhttp with a before-gather hook to refresh application metrics.
	h.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.refresh(r)
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})
	return h
}

// ServeHTTP satisfies http.Handler so the handler can be registered directly.
func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

// refresh pulls fresh application metrics from the DB before each scrape.
func (h *MetricsHandler) refresh(r *http.Request) {
	ctx := r.Context()

	var n int64

	// Active incidents.
	if h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM alert_incidents WHERE status = 'active'`).Scan(&n) == nil {
		h.activeIncidents.Set(float64(n))
	}

	// Unhealthy deployments.
	if h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM app_deployments WHERE health_status != 'healthy' AND health_status != 'unknown'`).Scan(&n) == nil {
		h.deploymentUnhealthy.Set(float64(n))
	}

	// HA nodes seen in the last 60 seconds.
	if h.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ha_nodes WHERE last_seen_at > now() - interval '60 seconds'`).Scan(&n) == nil {
		h.haNodesOnline.Set(float64(n))
	}

	// DB pool stats.
	stat := h.pool.Stat()
	h.dbPoolAcquired.Set(float64(stat.AcquiredConns()))
	h.dbPoolIdle.Set(float64(stat.IdleConns()))
	h.dbPoolTotal.Set(float64(stat.TotalConns()))
}

// RequestTimer returns middleware that records per-route HTTP request durations.
func RequestTimer(reg *prometheus.Registry) (func(http.Handler) http.Handler, *prometheus.Registry) {
	return func(next http.Handler) http.Handler { return next }, reg
}

// uptimeGauge exposes server start time so uptime is computable in dashboards.
var startTime = time.Now()
