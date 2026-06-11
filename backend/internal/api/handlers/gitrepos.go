package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	vaultpkg "github.com/entkube/entkube/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GitRepoHandler serves /api/tenants/{tenantID}/git-repos/*.
type GitRepoHandler struct {
	pool     *pgxpool.Pool
	vaultSvc *vaultpkg.Service
	selfNode uuid.UUID
}

// NewGitRepoHandler creates a GitRepoHandler.
func NewGitRepoHandler(pool *pgxpool.Pool, vaultSvc *vaultpkg.Service, selfNode uuid.UUID) *GitRepoHandler {
	return &GitRepoHandler{pool: pool, vaultSvc: vaultSvc, selfNode: selfNode}
}

type gitRepoDTO struct {
	ID            uuid.UUID `json:"id"`
	TenantID      uuid.UUID `json:"tenant_id"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	AuthType      string    `json:"auth_type"`
	Username      *string   `json:"username,omitempty"`
	DefaultBranch string    `json:"default_branch"`
	CreatedAt     time.Time `json:"created_at"`
}

// ── GET /api/tenants/{tenantID}/git-repos ────────────────────────

func (h *GitRepoHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, tenant_id, name, url, auth_type::text, username, default_branch, created_at
		FROM   git_repositories
		WHERE  tenant_id = $1
		ORDER  BY name`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var repos []gitRepoDTO
	for rows.Next() {
		var r gitRepoDTO
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &r.URL, &r.AuthType,
			&r.Username, &r.DefaultBranch, &r.CreatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		repos = append(repos, r)
	}
	if rows.Err() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

// ── POST /api/tenants/{tenantID}/git-repos ───────────────────────

func (h *GitRepoHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}

	var body struct {
		Name          string `json:"name"`
		URL           string `json:"url"`
		AuthType      string `json:"auth_type"` // none | https_pat | https_password | ssh_key
		Username      string `json:"username"`
		Credential    string `json:"credential"` // PAT / password / SSH key — stored in vault
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		body.Name == "" || body.URL == "" {
		http.Error(w, "name and url required", http.StatusBadRequest)
		return
	}
	if body.AuthType == "" {
		body.AuthType = "none"
	}
	if body.DefaultBranch == "" {
		body.DefaultBranch = "main"
	}

	// Store credential in vault if provided.
	var credVaultKey *string
	if body.Credential != "" {
		keyName := "git-credential/" + body.Name
		if err := h.storeCredential(r, tenantID, keyName, body.Credential); err != nil {
			http.Error(w, "store credential: "+err.Error(), http.StatusInternalServerError)
			return
		}
		credVaultKey = &keyName
	}

	var repo gitRepoDTO
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO git_repositories
			(tenant_id, name, url, auth_type, username, credential_vault_key, default_branch, origin_node_id)
		VALUES ($1, $2, $3, $4::git_auth_type, $5, $6, $7, $8)
		RETURNING id, tenant_id, name, url, auth_type::text, username, default_branch, created_at`,
		tenantID, body.Name, body.URL, body.AuthType,
		nullableStr(body.Username, body.Username != ""),
		credVaultKey, body.DefaultBranch, h.selfNode).
		Scan(&repo.ID, &repo.TenantID, &repo.Name, &repo.URL, &repo.AuthType,
			&repo.Username, &repo.DefaultBranch, &repo.CreatedAt)
	if err != nil {
		http.Error(w, "could not create git repo (name may already exist): "+err.Error(),
			http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

// ── DELETE /api/tenants/{tenantID}/git-repos/{repoID} ────────────

func (h *GitRepoHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	repoID, err := uuid.Parse(r.PathValue("repoID"))
	if err != nil {
		http.Error(w, "invalid repoID", http.StatusBadRequest)
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM git_repositories WHERE id = $1 AND tenant_id = $2`, repoID, tenantID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// storeCredential writes a git credential into the tenant vault.
func (h *GitRepoHandler) storeCredential(r *http.Request, tenantID uuid.UUID, keyName, credential string) error {
	var encDEK, nonce []byte
	if err := h.pool.QueryRow(r.Context(), `
		SELECT vnk.encrypted_dek, vnk.nonce
		FROM   vault_node_keys vnk
		JOIN   secret_vaults sv ON sv.id = vnk.vault_id
		WHERE  sv.tenant_id = $1 AND vnk.node_id = $2`,
		tenantID, h.selfNode).Scan(&encDEK, &nonce); err != nil {
		return err
	}
	dek, err := h.vaultSvc.UnsealDEK(encDEK, nonce)
	if err != nil {
		return err
	}
	encVal, valNonce, err := h.vaultSvc.EncryptSecret(dek, []byte(credential))
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
