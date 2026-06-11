package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	authpkg "github.com/entkube/entkube/internal/auth"
	vaultpkg "github.com/entkube/entkube/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VaultHandler serves /api/tenants/{tenantID}/secrets/*.
type VaultHandler struct {
	pool     *pgxpool.Pool
	svc      *vaultpkg.Service
	selfNode uuid.UUID
}

// NewVaultHandler creates a VaultHandler.
func NewVaultHandler(pool *pgxpool.Pool, svc *vaultpkg.Service, selfNode uuid.UUID) *VaultHandler {
	return &VaultHandler{pool: pool, svc: svc, selfNode: selfNode}
}

// ────────────────────────────────────────────────────────────────
// GET /api/tenants/{tenantID}/secrets
// ────────────────────────────────────────────────────────────────

func (h *VaultHandler) ListSecrets(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	if !h.checkTenantAccess(r, w, tenantID) {
		return
	}

	dek, err := h.loadDEK(r.Context(), tenantID)
	if err != nil {
		slog.Error("load DEK", "tenant", tenantID, "err", err)
		http.Error(w, "vault unavailable", http.StatusInternalServerError)
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, key_name, encrypted_value, value_nonce, updated_at
		FROM vault_secrets
		WHERE vault_id = (SELECT id FROM secret_vaults WHERE tenant_id = $1)
		  AND deleted_at IS NULL
		ORDER BY key_name`, tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type secretDTO struct {
		ID        uuid.UUID `json:"id"`
		KeyName   string    `json:"key_name"`
		Value     string    `json:"value"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	var secrets []secretDTO
	for rows.Next() {
		var id uuid.UUID
		var keyName string
		var encVal, nonce []byte
		var updatedAt time.Time
		if err := rows.Scan(&id, &keyName, &encVal, &nonce, &updatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		plaintext, err := h.svc.DecryptSecret(dek, nonce, encVal)
		if err != nil {
			slog.Error("decrypt secret", "key", keyName, "err", err)
			continue
		}
		secrets = append(secrets, secretDTO{ID: id, KeyName: keyName, Value: string(plaintext), UpdatedAt: updatedAt})
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, secrets)
}

// ────────────────────────────────────────────────────────────────
// PUT /api/tenants/{tenantID}/secrets/{keyName}
// ────────────────────────────────────────────────────────────────

func (h *VaultHandler) UpsertSecret(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	if !h.checkTenantAccess(r, w, tenantID) {
		return
	}

	keyName := r.PathValue("keyName")
	if keyName == "" {
		http.Error(w, "keyName required", http.StatusBadRequest)
		return
	}

	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	vaultID, dek, err := h.ensureVault(r.Context(), tenantID)
	if err != nil {
		http.Error(w, "vault error", http.StatusInternalServerError)
		return
	}

	encVal, nonce, err := h.svc.EncryptSecret(dek, []byte(body.Value))
	if err != nil {
		http.Error(w, "encryption error", http.StatusInternalServerError)
		return
	}

	_, err = h.pool.Exec(r.Context(), `
		INSERT INTO vault_secrets (vault_id, key_name, encrypted_value, value_nonce, origin_node_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (vault_id, key_name) DO UPDATE SET
			encrypted_value = EXCLUDED.encrypted_value,
			value_nonce     = EXCLUDED.value_nonce,
			updated_at      = now(),
			deleted_at      = NULL`,
		vaultID, keyName, encVal, nonce, h.selfNode)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ────────────────────────────────────────────────────────────────
// DELETE /api/tenants/{tenantID}/secrets/{keyName}
// ────────────────────────────────────────────────────────────────

func (h *VaultHandler) DeleteSecret(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := parseTenantID(r, w)
	if !ok {
		return
	}
	if !h.checkTenantAccess(r, w, tenantID) {
		return
	}

	keyName := r.PathValue("keyName")
	_, err := h.pool.Exec(r.Context(), `
		UPDATE vault_secrets SET deleted_at = now(), updated_at = now()
		WHERE vault_id = (SELECT id FROM secret_vaults WHERE tenant_id = $1)
		  AND key_name = $2 AND deleted_at IS NULL`,
		tenantID, keyName)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ────────────────────────────────────────────────────────────────
// ha.VaultSyncer implementation
// ────────────────────────────────────────────────────────────────

// ApplyVaultKeyTransport is intentionally not implemented here.
// Use vault.SyncService (wired in server.go) which has access to NodeManager.

// ────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────

func (h *VaultHandler) loadDEK(ctx context.Context, tenantID uuid.UUID) ([]byte, error) {
	var encDEK, nonce []byte
	err := h.pool.QueryRow(ctx, `
		SELECT vnk.encrypted_dek, vnk.nonce
		FROM vault_node_keys vnk
		JOIN secret_vaults sv ON sv.id = vnk.vault_id
		WHERE sv.tenant_id = $1 AND vnk.node_id = $2`,
		tenantID, h.selfNode).Scan(&encDEK, &nonce)
	if err != nil {
		return nil, fmt.Errorf("no DEK for tenant %s on this node: %w", tenantID, err)
	}
	return h.svc.UnsealDEK(encDEK, nonce)
}

// ensureVault returns the vault ID and DEK for a tenant, creating them if absent.
func (h *VaultHandler) ensureVault(ctx context.Context, tenantID uuid.UUID) (uuid.UUID, []byte, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, nil, err
	}
	defer tx.Rollback(ctx)

	var vaultID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM secret_vaults WHERE tenant_id = $1`, tenantID).Scan(&vaultID)
	if err != nil {
		// Create vault + DEK for this node.
		vaultID = uuid.New()
		dek, err := h.svc.GenerateDEK()
		if err != nil {
			return uuid.Nil, nil, err
		}
		encDEK, nonce, err := h.svc.SealDEK(dek)
		if err != nil {
			return uuid.Nil, nil, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO secret_vaults (id, tenant_id, origin_node_id) VALUES ($1, $2, $3)`,
			vaultID, tenantID, h.selfNode); err != nil {
			return uuid.Nil, nil, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO vault_node_keys (vault_id, node_id, encrypted_dek, nonce) VALUES ($1, $2, $3, $4)`,
			vaultID, h.selfNode, encDEK, nonce); err != nil {
			return uuid.Nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, nil, err
		}
		return vaultID, dek, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, nil, err
	}

	dek, err := h.loadDEK(ctx, tenantID)
	return vaultID, dek, err
}

func parseTenantID(r *http.Request, w http.ResponseWriter) (uuid.UUID, bool) {
	raw := r.PathValue("tenantID")
	id, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, "invalid tenantID", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

func (h *VaultHandler) checkTenantAccess(r *http.Request, w http.ResponseWriter, tenantID uuid.UUID) bool {
	claims := authpkg.ClaimsFromCtx(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if claims.IsAdmin {
		return true
	}
	var exists bool
	_ = h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM tenant_memberships WHERE user_id = $1 AND tenant_id = $2)`,
		claims.UserID, tenantID).Scan(&exists)
	if !exists {
		http.Error(w, "forbidden", http.StatusForbidden)
	}
	return exists
}
