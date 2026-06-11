package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/entkube/entkube/internal/k8s"
	vaultpkg "github.com/entkube/entkube/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ClusterHandler serves /api/tenants/{tenantID}/clusters/*.
type ClusterHandler struct {
	pool     *pgxpool.Pool
	vaultSvc *vaultpkg.Service
	selfNode uuid.UUID
}

// NewClusterHandler creates a ClusterHandler.
func NewClusterHandler(pool *pgxpool.Pool, vaultSvc *vaultpkg.Service, selfNode uuid.UUID) *ClusterHandler {
	return &ClusterHandler{pool: pool, vaultSvc: vaultSvc, selfNode: selfNode}
}

type clusterDTO struct {
	ID            uuid.UUID `json:"id"`
	TenantID      uuid.UUID `json:"tenant_id"`
	EnvironmentID uuid.UUID `json:"environment_id"`
	Name          string    `json:"name"`
	APIServerURL  string    `json:"api_server_url"`
	CreatedAt     time.Time `json:"created_at"`
}

// ── GET /api/tenants/{tenantID}/clusters ─────────────────────────

func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, tenant_id, environment_id, name, api_server_url, created_at
		FROM   kubernetes_clusters
		WHERE  tenant_id = $1
		ORDER  BY name`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var clusters []clusterDTO
	for rows.Next() {
		var c clusterDTO
		if err := rows.Scan(&c.ID, &c.TenantID, &c.EnvironmentID, &c.Name, &c.APIServerURL, &c.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		clusters = append(clusters, c)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, clusters)
}

// ── POST /api/tenants/{tenantID}/clusters ────────────────────────

func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	var body struct {
		EnvironmentID uuid.UUID `json:"environment_id"`
		Name          string    `json:"name"`
		APIServerURL  string    `json:"api_server_url"`
		Kubeconfig    string    `json:"kubeconfig"` // raw YAML — stored in vault
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Name == "" || body.APIServerURL == "" {
		http.Error(w, "name, api_server_url and environment_id required", http.StatusBadRequest)
		return
	}

	// Validate kubeconfig can connect.
	if body.Kubeconfig != "" {
		if _, err := k8s.New(body.Kubeconfig); err != nil {
			http.Error(w, "invalid kubeconfig: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Store kubeconfig in vault and keep only the key name in the cluster row.
	vaultKeyName := "kubeconfig/" + body.Name
	if body.Kubeconfig != "" {
		if err := h.storeKubeconfig(r, tenantID, vaultKeyName, body.Kubeconfig); err != nil {
			http.Error(w, "store kubeconfig: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	var c clusterDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO kubernetes_clusters (tenant_id, environment_id, name, api_server_url, kubeconfig_vault_key, origin_node_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, tenant_id, environment_id, name, api_server_url, created_at`,
		tenantID, body.EnvironmentID, body.Name, body.APIServerURL,
		nullableStr(vaultKeyName, body.Kubeconfig != ""), h.selfNode).
		Scan(&c.ID, &c.TenantID, &c.EnvironmentID, &c.Name, &c.APIServerURL, &c.CreatedAt)
	if err != nil {
		http.Error(w, "could not create cluster", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, c)
}

// ── GET /api/tenants/{tenantID}/clusters/{clusterID}/namespaces ──

func (h *ClusterHandler) ListNamespaces(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(r, w)
	if !ok {
		return
	}

	kubeconfig, err := h.loadKubeconfig(r, tenantID, clusterID)
	if err != nil {
		http.Error(w, "load kubeconfig: "+err.Error(), http.StatusInternalServerError)
		return
	}

	client, err := k8s.New(kubeconfig)
	if err != nil {
		http.Error(w, "connect to cluster: "+err.Error(), http.StatusBadGateway)
		return
	}

	namespaces, err := client.ListNamespaces(r.Context())
	if err != nil {
		http.Error(w, "list namespaces: "+err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, namespaces)
}

// ────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────

func (h *ClusterHandler) storeKubeconfig(r *http.Request, tenantID uuid.UUID, keyName, kubeconfig string) error {
	// Ensure vault exists, get DEK.
	var encDEK, nonce []byte
	err := h.pool.QueryRow(r.Context(), `
		SELECT vnk.encrypted_dek, vnk.nonce
		FROM   vault_node_keys vnk
		JOIN   secret_vaults sv ON sv.id = vnk.vault_id
		WHERE  sv.tenant_id = $1 AND vnk.node_id = $2`,
		tenantID, h.selfNode).Scan(&encDEK, &nonce)
	if err != nil {
		return err
	}
	dek, err := h.vaultSvc.UnsealDEK(encDEK, nonce)
	if err != nil {
		return err
	}

	encVal, valNonce, err := h.vaultSvc.EncryptSecret(dek, []byte(kubeconfig))
	if err != nil {
		return err
	}

	var vaultID uuid.UUID
	_ = h.pool.QueryRow(r.Context(),
		`SELECT id FROM secret_vaults WHERE tenant_id = $1`, tenantID).Scan(&vaultID)

	_, err = h.pool.Exec(r.Context(), `
		INSERT INTO vault_secrets (vault_id, key_name, encrypted_value, value_nonce, origin_node_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (vault_id, key_name) DO UPDATE SET
			encrypted_value = EXCLUDED.encrypted_value,
			value_nonce     = EXCLUDED.value_nonce,
			updated_at      = now()`,
		vaultID, keyName, encVal, valNonce, h.selfNode)
	return err
}

func (h *ClusterHandler) loadKubeconfig(r *http.Request, tenantID, clusterID uuid.UUID) (string, error) {
	var vaultKey *string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT kubeconfig_vault_key FROM kubernetes_clusters WHERE id = $1 AND tenant_id = $2`,
		clusterID, tenantID).Scan(&vaultKey); err != nil {
		return "", err
	}
	if vaultKey == nil {
		return "", nil
	}
	// Reuse the deployment sync helper logic via the vault service.
	var encDEK, nonce []byte
	if err := h.pool.QueryRow(r.Context(), `
		SELECT vnk.encrypted_dek, vnk.nonce
		FROM   vault_node_keys vnk
		JOIN   secret_vaults sv ON sv.id = vnk.vault_id
		WHERE  sv.tenant_id = $1 AND vnk.node_id = $2`,
		tenantID, h.selfNode).Scan(&encDEK, &nonce); err != nil {
		return "", err
	}
	dek, err := h.vaultSvc.UnsealDEK(encDEK, nonce)
	if err != nil {
		return "", err
	}
	var encVal, valNonce []byte
	if err := h.pool.QueryRow(r.Context(), `
		SELECT vs.encrypted_value, vs.value_nonce
		FROM   vault_secrets vs
		JOIN   secret_vaults sv ON sv.id = vs.vault_id
		WHERE  sv.tenant_id = $1 AND vs.key_name = $2 AND vs.deleted_at IS NULL`,
		tenantID, *vaultKey).Scan(&encVal, &valNonce); err != nil {
		return "", err
	}
	plain, err := h.vaultSvc.DecryptSecret(dek, valNonce, encVal)
	return string(plain), err
}

func parseClusterID(r *http.Request, w http.ResponseWriter) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("clusterID"))
	if err != nil {
		http.Error(w, "invalid clusterID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

// nullableStr returns &s if condition is true, otherwise nil.
func nullableStr(s string, condition bool) *string {
	if !condition {
		return nil
	}
	return &s
}
