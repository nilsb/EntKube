package api

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/entkube/entkube/internal/api/handlers"
	authpkg "github.com/entkube/entkube/internal/auth"
	"github.com/entkube/entkube/internal/ha"
	vaultpkg "github.com/entkube/entkube/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Server is the HTTP server with all routes wired up.
type Server struct {
	handler http.Handler
	port    int
}

// New builds the Server, registers all routes, and applies middleware.
func New(
	pool *pgxpool.Pool,
	authSvc *authpkg.Service,
	nodeMgr *ha.NodeManager,
	vaultSvc *vaultpkg.Service,
	selfNode uuid.UUID,
	joinToken string,
	gitSyncQueue func(uuid.UUID),
	staticDir string,
	port int,
) *Server {
	mux := http.NewServeMux()

	authH := handlers.NewAuthHandler(authSvc)
	haH := handlers.NewHAHandler(pool, nodeMgr, vaultSvc, joinToken)
	vaultH := handlers.NewVaultHandler(pool, vaultSvc, selfNode)
	tenantH := handlers.NewTenantHandler(pool, selfNode)
	userH := handlers.NewUserHandler(pool, authSvc, selfNode)
	clusterH := handlers.NewClusterHandler(pool, vaultSvc, selfNode)
	deployH := handlers.NewDeploymentHandler(pool, selfNode, gitSyncQueue)
	customerH := handlers.NewCustomerHandler(pool, selfNode)
	appH := handlers.NewAppHandler(pool, selfNode)
	alertH := handlers.NewAlertHandler(pool)
	envH := handlers.NewEnvironmentHandler(pool, selfNode)
	webhookH := handlers.NewWebhookHandler(pool, gitSyncQueue)
	backupH := handlers.NewBackupHandler(pool, vaultSvc, selfNode)
	notifH := handlers.NewNotificationHandler(pool, selfNode)
	gitRepoH := handlers.NewGitRepoHandler(pool, vaultSvc, selfNode)
	componentH := handlers.NewComponentHandler(pool, selfNode)
	maintenanceH := handlers.NewMaintenanceHandler(pool, selfNode)
	alertRuleH := handlers.NewAlertRuleHandler(pool, selfNode)
	metricsH := handlers.NewMetricsHandler(pool, selfNode.String())

	authed := authSvc.Middleware
	admin := authSvc.AdminMiddleware

	// ── Public ─────────────────────────────────────────────────
	mux.HandleFunc("POST /api/auth/login", authH.Login)
	mux.HandleFunc("POST /api/auth/refresh", authH.Refresh)
	mux.HandleFunc("POST /api/auth/logout", authH.Logout)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("GET /metrics", metricsH)

	// Git webhook (authenticated via secret in URL path)
	mux.HandleFunc("POST /api/webhooks/git/{secret}", webhookH.Git)

	// ── HA inter-node (token/HMAC authenticated) ───────────────
	mux.HandleFunc("POST /api/ha/join", haH.Join)
	mux.HandleFunc("POST /api/ha/sync", haH.Sync)

	// ── Authenticated user ──────────────────────────────────────
	mux.Handle("GET /api/auth/me", authed(http.HandlerFunc(authH.Me)))
	mux.Handle("GET /api/ha/nodes", admin(http.HandlerFunc(haH.Nodes)))

	// Users (admin)
	mux.Handle("GET /api/users",             admin(http.HandlerFunc(userH.List)))
	mux.Handle("POST /api/users",            admin(http.HandlerFunc(userH.Create)))
	mux.Handle("GET /api/users/{userID}",    admin(http.HandlerFunc(userH.Get)))
	mux.Handle("PATCH /api/users/{userID}",  admin(http.HandlerFunc(userH.Update)))
	mux.Handle("DELETE /api/users/{userID}", admin(http.HandlerFunc(userH.Delete)))

	// Tenants
	mux.Handle("GET /api/tenants",                 authed(http.HandlerFunc(tenantH.List)))
	mux.Handle("POST /api/tenants",                admin(http.HandlerFunc(tenantH.Create)))
	mux.Handle("GET /api/tenants/{tenantID}",       authed(http.HandlerFunc(tenantH.Get)))
	mux.Handle("DELETE /api/tenants/{tenantID}",    admin(http.HandlerFunc(tenantH.Delete)))

	// Tenant members
	mux.Handle("POST /api/tenants/{tenantID}/members",
		admin(http.HandlerFunc(userH.AddMember)))
	mux.Handle("DELETE /api/tenants/{tenantID}/members/{userID}",
		admin(http.HandlerFunc(userH.RemoveMember)))

	// Environments
	mux.Handle("GET /api/tenants/{tenantID}/environments",
		authed(http.HandlerFunc(envH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/environments",
		authed(http.HandlerFunc(envH.Create)))
	mux.Handle("DELETE /api/tenants/{tenantID}/environments/{envID}",
		admin(http.HandlerFunc(envH.Delete)))

	// Customers
	mux.Handle("GET /api/tenants/{tenantID}/customers",
		authed(http.HandlerFunc(customerH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/customers",
		authed(http.HandlerFunc(customerH.Create)))
	mux.Handle("GET /api/tenants/{tenantID}/customers/{customerID}",
		authed(http.HandlerFunc(customerH.Get)))
	mux.Handle("DELETE /api/tenants/{tenantID}/customers/{customerID}",
		admin(http.HandlerFunc(customerH.Delete)))

	// Apps (under customers)
	mux.Handle("GET /api/tenants/{tenantID}/customers/{customerID}/apps",
		authed(http.HandlerFunc(appH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/customers/{customerID}/apps",
		authed(http.HandlerFunc(appH.Create)))
	mux.Handle("DELETE /api/tenants/{tenantID}/customers/{customerID}/apps/{appID}",
		admin(http.HandlerFunc(appH.Delete)))

	// Vault secrets (per tenant)
	mux.Handle("GET /api/tenants/{tenantID}/secrets",
		authed(http.HandlerFunc(vaultH.ListSecrets)))
	mux.Handle("PUT /api/tenants/{tenantID}/secrets/{keyName}",
		authed(http.HandlerFunc(vaultH.UpsertSecret)))
	mux.Handle("DELETE /api/tenants/{tenantID}/secrets/{keyName}",
		authed(http.HandlerFunc(vaultH.DeleteSecret)))

	// Git repositories
	mux.Handle("GET /api/tenants/{tenantID}/git-repos",
		authed(http.HandlerFunc(gitRepoH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/git-repos",
		authed(http.HandlerFunc(gitRepoH.Create)))
	mux.Handle("DELETE /api/tenants/{tenantID}/git-repos/{repoID}",
		authed(http.HandlerFunc(gitRepoH.Delete)))

	// Kubernetes clusters
	mux.Handle("GET /api/tenants/{tenantID}/clusters",
		authed(http.HandlerFunc(clusterH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/clusters",
		authed(http.HandlerFunc(clusterH.Create)))
	mux.Handle("GET /api/tenants/{tenantID}/clusters/{clusterID}/namespaces",
		authed(http.HandlerFunc(clusterH.ListNamespaces)))

	// Cluster components
	mux.Handle("GET /api/tenants/{tenantID}/clusters/{clusterID}/components",
		authed(http.HandlerFunc(componentH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/clusters/{clusterID}/components",
		authed(http.HandlerFunc(componentH.Create)))
	mux.Handle("PATCH /api/tenants/{tenantID}/clusters/{clusterID}/components/{componentID}",
		authed(http.HandlerFunc(componentH.UpdateStatus)))
	mux.Handle("DELETE /api/tenants/{tenantID}/clusters/{clusterID}/components/{componentID}",
		authed(http.HandlerFunc(componentH.Delete)))

	// Deployments
	mux.Handle("GET /api/tenants/{tenantID}/deployments",
		authed(http.HandlerFunc(deployH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/deployments",
		authed(http.HandlerFunc(deployH.Create)))
	mux.Handle("POST /api/tenants/{tenantID}/deployments/{deploymentID}/sync",
		authed(http.HandlerFunc(deployH.TriggerSync)))
	mux.Handle("GET /api/tenants/{tenantID}/deployments/{deploymentID}/resources",
		authed(http.HandlerFunc(deployH.ListResources)))

	// Alert incidents
	mux.Handle("GET /api/tenants/{tenantID}/alerts",
		authed(http.HandlerFunc(alertH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/alerts/{alertID}/acknowledge",
		authed(http.HandlerFunc(alertH.Acknowledge)))
	mux.Handle("POST /api/tenants/{tenantID}/alerts/{alertID}/resolve",
		authed(http.HandlerFunc(alertH.Resolve)))
	mux.Handle("GET /api/tenants/{tenantID}/alerts/{alertID}/notes",
		authed(http.HandlerFunc(alertH.ListNotes)))
	mux.Handle("POST /api/tenants/{tenantID}/alerts/{alertID}/notes",
		authed(http.HandlerFunc(alertH.AddNote)))

	// Notification channels
	mux.Handle("GET /api/tenants/{tenantID}/notification-channels",
		authed(http.HandlerFunc(notifH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/notification-channels",
		authed(http.HandlerFunc(notifH.Create)))
	mux.Handle("DELETE /api/tenants/{tenantID}/notification-channels/{channelID}",
		authed(http.HandlerFunc(notifH.Delete)))
	mux.Handle("POST /api/tenants/{tenantID}/notification-channels/{channelID}/test",
		authed(http.HandlerFunc(notifH.Test)))

	// Maintenance windows
	mux.Handle("GET /api/tenants/{tenantID}/maintenance-windows",
		authed(http.HandlerFunc(maintenanceH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/maintenance-windows",
		authed(http.HandlerFunc(maintenanceH.Create)))
	mux.Handle("DELETE /api/tenants/{tenantID}/maintenance-windows/{windowID}",
		authed(http.HandlerFunc(maintenanceH.Delete)))

	// Alert routing rules
	mux.Handle("GET /api/tenants/{tenantID}/alert-rules",
		authed(http.HandlerFunc(alertRuleH.List)))
	mux.Handle("POST /api/tenants/{tenantID}/alert-rules",
		authed(http.HandlerFunc(alertRuleH.Create)))
	mux.Handle("PATCH /api/tenants/{tenantID}/alert-rules/{ruleID}",
		authed(http.HandlerFunc(alertRuleH.ToggleEnabled)))
	mux.Handle("DELETE /api/tenants/{tenantID}/alert-rules/{ruleID}",
		authed(http.HandlerFunc(alertRuleH.Delete)))

	// Backup / restore (admin)
	mux.Handle("GET /api/admin/backup",    admin(http.HandlerFunc(backupH.Export)))
	mux.Handle("POST /api/admin/restore",  admin(http.HandlerFunc(backupH.Import)))

	// ── SPA fallback ────────────────────────────────────────────
	mux.Handle("/", spaHandler(staticDir))

	return &Server{
		handler: corsMiddleware(loggingMiddleware(mux)),
		port:    port,
	}
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf(":%d", s.port)
	slog.Info("starting HTTP server", "addr", addr)
	return http.ListenAndServe(addr, s.handler)
}

// spaHandler serves static files from dir. Requests for unknown paths fall
// back to index.html so client-side routing works.
func spaHandler(dir string) http.Handler {
	fileServer := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only serve GET/HEAD for the SPA.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		// Check if the file exists.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := os.Stat(dir + "/" + path); os.IsNotExist(err) || path == "" {
			// Serve index.html for all unknown paths (client-side routing).
			http.ServeFile(w, r, dir+"/index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// Suppress unused import warning when static dir doesn't exist at compile time.
var _ fs.FS

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
