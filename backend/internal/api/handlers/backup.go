package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	vaultpkg "github.com/entkube/entkube/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BackupHandler serves GET /api/admin/backup and POST /api/admin/restore.
// The export is a JSON document containing all non-HA application state.
// Vault secrets are exported re-encrypted under a backup key derived from
// the caller-supplied passphrase (future work); for now they are excluded
// and a flag marks their presence so the operator knows to re-enter them.
type BackupHandler struct {
	pool     *pgxpool.Pool
	vaultSvc *vaultpkg.Service
	selfNode uuid.UUID
}

// NewBackupHandler creates a BackupHandler.
func NewBackupHandler(pool *pgxpool.Pool, vaultSvc *vaultpkg.Service, selfNode uuid.UUID) *BackupHandler {
	return &BackupHandler{pool: pool, vaultSvc: vaultSvc, selfNode: selfNode}
}

// BackupDocument is the top-level export structure.
type BackupDocument struct {
	Version      int              `json:"version"`
	ExportedAt   time.Time        `json:"exported_at"`
	Tenants      []backupTenant   `json:"tenants"`
	Users        []backupUser     `json:"users"`
	Memberships  []backupMembership `json:"memberships"`
}

type backupTenant struct {
	ID           uuid.UUID          `json:"id"`
	Name         string             `json:"name"`
	Slug         string             `json:"slug"`
	CreatedAt    time.Time          `json:"created_at"`
	Environments []backupEnv        `json:"environments"`
	Customers    []backupCustomer   `json:"customers"`
	Clusters     []backupCluster    `json:"clusters"`
	Secrets      []backupSecretMeta `json:"secrets"` // key names only — values excluded
	GitRepos     []backupGitRepo    `json:"git_repos"`
	NotifChans   []backupNotifChan  `json:"notification_channels"`
}

type backupEnv struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type backupCustomer struct {
	ID        uuid.UUID   `json:"id"`
	Name      string      `json:"name"`
	CreatedAt time.Time   `json:"created_at"`
	Apps      []backupApp `json:"apps"`
}

type backupApp struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Namespace   *string     `json:"namespace,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	Deployments []backupDeployment `json:"deployments"`
}

type backupDeployment struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	DeploymentType string     `json:"deployment_type"`
	Namespace      string     `json:"namespace"`
	ClusterID      uuid.UUID  `json:"cluster_id"`
	EnvironmentID  uuid.UUID  `json:"environment_id"`
	GitURL         *string    `json:"git_url,omitempty"`
	GitRevision    string     `json:"git_revision"`
	GitPath        string     `json:"git_path"`
	GitAutoSync    bool       `json:"git_auto_sync"`
	HelmRepoURL    *string    `json:"helm_repo_url,omitempty"`
	HelmChartName  *string    `json:"helm_chart_name,omitempty"`
	HelmChartVersion *string  `json:"helm_chart_version,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type backupCluster struct {
	ID            uuid.UUID `json:"id"`
	EnvironmentID uuid.UUID `json:"environment_id"`
	Name          string    `json:"name"`
	APIServerURL  string    `json:"api_server_url"`
	CreatedAt     time.Time `json:"created_at"`
	// kubeconfig is a vault secret — excluded from backup
	KubeconfigVaultKey *string `json:"kubeconfig_vault_key,omitempty"`
}

type backupSecretMeta struct {
	KeyName   string    `json:"key_name"`
	UpdatedAt time.Time `json:"updated_at"`
}

type backupGitRepo struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	AuthType      string    `json:"auth_type"`
	Username      *string   `json:"username,omitempty"`
	DefaultBranch string    `json:"default_branch"`
	CreatedAt     time.Time `json:"created_at"`
}

type backupNotifChan struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	ChannelType    string    `json:"channel_type"`
	ConfigJSON     string    `json:"config_json"`
	IsEnabled      bool      `json:"is_enabled"`
	SeverityFilter string    `json:"severity_filter"`
	CreatedAt      time.Time `json:"created_at"`
}

type backupUser struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

type backupMembership struct {
	UserID   uuid.UUID `json:"user_id"`
	TenantID uuid.UUID `json:"tenant_id"`
	Role     string    `json:"role"`
}

// ── GET /api/admin/backup ────────────────────────────────────────

func (h *BackupHandler) Export(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	doc := BackupDocument{
		Version:    1,
		ExportedAt: time.Now().UTC(),
	}

	// Users
	rows, err := h.pool.Query(ctx,
		`SELECT id, email, is_admin, created_at FROM users WHERE deleted_at IS NULL ORDER BY email`)
	if err != nil {
		http.Error(w, "export users: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var u backupUser
		if err := rows.Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt); err != nil {
			rows.Close()
			http.Error(w, "scan user: "+err.Error(), http.StatusInternalServerError)
			return
		}
		doc.Users = append(doc.Users, u)
	}
	rows.Close()

	// Memberships
	mrows, err := h.pool.Query(ctx,
		`SELECT user_id, tenant_id, role FROM tenant_memberships ORDER BY tenant_id`)
	if err != nil {
		http.Error(w, "export memberships: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for mrows.Next() {
		var m backupMembership
		if err := mrows.Scan(&m.UserID, &m.TenantID, &m.Role); err != nil {
			mrows.Close()
			http.Error(w, "scan membership: "+err.Error(), http.StatusInternalServerError)
			return
		}
		doc.Memberships = append(doc.Memberships, m)
	}
	mrows.Close()

	// Tenants + all sub-entities
	trows, err := h.pool.Query(ctx,
		`SELECT id, name, slug, created_at FROM tenants WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		http.Error(w, "export tenants: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var tenantIDs []uuid.UUID
	tenantMap := map[uuid.UUID]*backupTenant{}
	for trows.Next() {
		var t backupTenant
		if err := trows.Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedAt); err != nil {
			trows.Close()
			http.Error(w, "scan tenant: "+err.Error(), http.StatusInternalServerError)
			return
		}
		doc.Tenants = append(doc.Tenants, t)
		tenantIDs = append(tenantIDs, t.ID)
		tenantMap[t.ID] = &doc.Tenants[len(doc.Tenants)-1]
	}
	trows.Close()

	for _, tid := range tenantIDs {
		t := tenantMap[tid]

		// Environments
		erows, _ := h.pool.Query(ctx,
			`SELECT id, name, created_at FROM environments WHERE tenant_id = $1 ORDER BY name`, tid)
		for erows.Next() {
			var e backupEnv
			erows.Scan(&e.ID, &e.Name, &e.CreatedAt)
			t.Environments = append(t.Environments, e)
		}
		erows.Close()

		// Clusters
		clrows, _ := h.pool.Query(ctx, `
			SELECT id, environment_id, name, api_server_url, kubeconfig_vault_key, created_at
			FROM   kubernetes_clusters WHERE tenant_id = $1 ORDER BY name`, tid)
		for clrows.Next() {
			var c backupCluster
			clrows.Scan(&c.ID, &c.EnvironmentID, &c.Name, &c.APIServerURL, &c.KubeconfigVaultKey, &c.CreatedAt)
			t.Clusters = append(t.Clusters, c)
		}
		clrows.Close()

		// Secret key names only
		srows, _ := h.pool.Query(ctx, `
			SELECT vs.key_name, vs.updated_at
			FROM   vault_secrets vs
			JOIN   secret_vaults sv ON sv.id = vs.vault_id
			WHERE  sv.tenant_id = $1 AND vs.deleted_at IS NULL
			ORDER  BY vs.key_name`, tid)
		for srows.Next() {
			var s backupSecretMeta
			srows.Scan(&s.KeyName, &s.UpdatedAt)
			t.Secrets = append(t.Secrets, s)
		}
		srows.Close()

		// Git repos
		grrows, _ := h.pool.Query(ctx, `
			SELECT id, name, url, auth_type::text, username, default_branch, created_at
			FROM   git_repositories WHERE tenant_id = $1 ORDER BY name`, tid)
		for grrows.Next() {
			var g backupGitRepo
			grrows.Scan(&g.ID, &g.Name, &g.URL, &g.AuthType, &g.Username, &g.DefaultBranch, &g.CreatedAt)
			t.GitRepos = append(t.GitRepos, g)
		}
		grrows.Close()

		// Notification channels
		ncrows, _ := h.pool.Query(ctx, `
			SELECT id, name, channel_type::text, configuration_json, is_enabled,
			       severity_filter::text, created_at
			FROM   notification_channels WHERE tenant_id = $1 ORDER BY name`, tid)
		for ncrows.Next() {
			var nc backupNotifChan
			ncrows.Scan(&nc.ID, &nc.Name, &nc.ChannelType, &nc.ConfigJSON,
				&nc.IsEnabled, &nc.SeverityFilter, &nc.CreatedAt)
			t.NotifChans = append(t.NotifChans, nc)
		}
		ncrows.Close()

		// Customers → Apps → Deployments
		crows, _ := h.pool.Query(ctx,
			`SELECT id, name, created_at FROM customers WHERE tenant_id = $1 ORDER BY name`, tid)
		for crows.Next() {
			var cust backupCustomer
			crows.Scan(&cust.ID, &cust.Name, &cust.CreatedAt)

			// Apps
			arows, _ := h.pool.Query(ctx,
				`SELECT id, name, namespace, created_at FROM apps WHERE customer_id = $1 ORDER BY name`,
				cust.ID)
			for arows.Next() {
				var app backupApp
				arows.Scan(&app.ID, &app.Name, &app.Namespace, &app.CreatedAt)

				// Deployments
				drows, _ := h.pool.Query(ctx, `
					SELECT id, name, deployment_type::text, namespace,
					       cluster_id, environment_id,
					       git_url, git_revision, git_path, git_auto_sync,
					       helm_repo_url, helm_chart_name, helm_chart_version,
					       created_at
					FROM   app_deployments WHERE app_id = $1 ORDER BY name`, app.ID)
				for drows.Next() {
					var d backupDeployment
					drows.Scan(&d.ID, &d.Name, &d.DeploymentType, &d.Namespace,
						&d.ClusterID, &d.EnvironmentID,
						&d.GitURL, &d.GitRevision, &d.GitPath, &d.GitAutoSync,
						&d.HelmRepoURL, &d.HelmChartName, &d.HelmChartVersion,
						&d.CreatedAt)
					app.Deployments = append(app.Deployments, d)
				}
				drows.Close()

				cust.Apps = append(cust.Apps, app)
			}
			arows.Close()

			t.Customers = append(t.Customers, cust)
		}
		crows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="entkube-backup-%s.json"`,
		time.Now().UTC().Format("2006-01-02T150405"),
	))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(doc)
}

// ── POST /api/admin/restore ──────────────────────────────────────
// Restore is additive: it skips rows that already exist (by ID or unique key).
// It does NOT delete existing data. Secrets are not restored.

func (h *BackupHandler) Import(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var doc BackupDocument
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if doc.Version != 1 {
		http.Error(w, fmt.Sprintf("unsupported backup version %d", doc.Version), http.StatusBadRequest)
		return
	}

	stats := map[string]int{}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		http.Error(w, "begin tx: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	// Users
	for _, u := range doc.Users {
		tag, _ := tx.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, is_admin, created_at, origin_node_id)
			VALUES ($1, $2, '', $3, $4, $5)
			ON CONFLICT (id) DO NOTHING`,
			u.ID, u.Email, u.IsAdmin, u.CreatedAt, h.selfNode)
		stats["users"] += int(tag.RowsAffected())
	}

	// Tenants
	for _, t := range doc.Tenants {
		tag, _ := tx.Exec(ctx, `
			INSERT INTO tenants (id, name, slug, created_at, origin_node_id)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO NOTHING`,
			t.ID, t.Name, t.Slug, t.CreatedAt, h.selfNode)
		stats["tenants"] += int(tag.RowsAffected())

		// Environments
		for _, e := range t.Environments {
			tx.Exec(ctx, `
				INSERT INTO environments (id, tenant_id, name, created_at, origin_node_id)
				VALUES ($1, $2, $3, $4, $5) ON CONFLICT (id) DO NOTHING`,
				e.ID, t.ID, e.Name, e.CreatedAt, h.selfNode)
		}

		// Clusters
		for _, c := range t.Clusters {
			tx.Exec(ctx, `
				INSERT INTO kubernetes_clusters
					(id, tenant_id, environment_id, name, api_server_url, kubeconfig_vault_key, created_at, origin_node_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO NOTHING`,
				c.ID, t.ID, c.EnvironmentID, c.Name, c.APIServerURL, c.KubeconfigVaultKey, c.CreatedAt, h.selfNode)
		}

		// Git repos
		for _, g := range t.GitRepos {
			tx.Exec(ctx, `
				INSERT INTO git_repositories
					(id, tenant_id, name, url, auth_type, username, default_branch, created_at, origin_node_id)
				VALUES ($1,$2,$3,$4,$5::git_auth_type,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING`,
				g.ID, t.ID, g.Name, g.URL, g.AuthType, g.Username, g.DefaultBranch, g.CreatedAt, h.selfNode)
		}

		// Notification channels
		for _, nc := range t.NotifChans {
			tx.Exec(ctx, `
				INSERT INTO notification_channels
					(id, tenant_id, name, channel_type, configuration_json, is_enabled, severity_filter, created_at, origin_node_id)
				VALUES ($1,$2,$3,$4::notification_channel_type,$5,$6,$7::severity_filter,$8,$9)
				ON CONFLICT (id) DO NOTHING`,
				nc.ID, t.ID, nc.Name, nc.ChannelType, nc.ConfigJSON, nc.IsEnabled, nc.SeverityFilter, nc.CreatedAt, h.selfNode)
		}

		// Customers → Apps → Deployments
		for _, cust := range t.Customers {
			tx.Exec(ctx, `
				INSERT INTO customers (id, tenant_id, name, created_at, origin_node_id)
				VALUES ($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`,
				cust.ID, t.ID, cust.Name, cust.CreatedAt, h.selfNode)
			stats["customers"]++

			for _, app := range cust.Apps {
				tx.Exec(ctx, `
					INSERT INTO apps (id, customer_id, name, namespace, created_at, origin_node_id)
					VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (id) DO NOTHING`,
					app.ID, cust.ID, app.Name, app.Namespace, app.CreatedAt, h.selfNode)

				for _, d := range app.Deployments {
					tx.Exec(ctx, `
						INSERT INTO app_deployments
							(id, app_id, environment_id, cluster_id, name, deployment_type,
							 namespace, git_url, git_revision, git_path, git_auto_sync,
							 helm_repo_url, helm_chart_name, helm_chart_version, created_at, origin_node_id)
						VALUES ($1,$2,$3,$4,$5,$6::deployment_type,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
						ON CONFLICT (id) DO NOTHING`,
						d.ID, app.ID, d.EnvironmentID, d.ClusterID, d.Name, d.DeploymentType,
						d.Namespace, d.GitURL, d.GitRevision, d.GitPath, d.GitAutoSync,
						d.HelmRepoURL, d.HelmChartName, d.HelmChartVersion, d.CreatedAt, h.selfNode)
				}
			}
		}
	}

	// Memberships
	for _, m := range doc.Memberships {
		tx.Exec(ctx, `
			INSERT INTO tenant_memberships (user_id, tenant_id, role, origin_node_id)
			VALUES ($1,$2,$3,$4) ON CONFLICT (user_id, tenant_id) DO NOTHING`,
			m.UserID, m.TenantID, m.Role, h.selfNode)
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, "commit: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"note":   "Vault secrets were NOT restored — re-enter them manually.",
		"stats":  stats,
	})
}
