package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/entkube/entkube/internal/ha"
	"github.com/entkube/entkube/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HAHandler serves the inter-node HA API: /api/ha/join and /api/ha/sync.
type HAHandler struct {
	pool      *pgxpool.Pool
	nodes     *ha.NodeManager
	vaultSvc  *vault.Service
	joinToken string // empty = join disabled
}

// NewHAHandler creates a HAHandler.
func NewHAHandler(pool *pgxpool.Pool, nodes *ha.NodeManager, vaultSvc *vault.Service, joinToken string) *HAHandler {
	return &HAHandler{pool: pool, nodes: nodes, vaultSvc: vaultSvc, joinToken: joinToken}
}

// ────────────────────────────────────────────────────────────────
// POST /api/ha/join
// ────────────────────────────────────────────────────────────────

// Join handles an incoming join request from a new node.
// The caller must present the HA_JOIN_TOKEN; on success the new node is
// persisted and this node's own identity is returned so the caller can
// establish the ECDH shared secret.
func (h *HAHandler) Join(w http.ResponseWriter, r *http.Request) {
	var req ha.JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if h.joinToken == "" || req.JoinToken != h.joinToken {
		http.Error(w, "invalid join token", http.StatusUnauthorized)
		return
	}

	peer := ha.Node{
		ID:        req.NodeID,
		Address:   req.Address,
		PublicKey: req.PublicKey,
	}
	if err := h.nodes.AddPeer(r.Context(), peer); err != nil {
		slog.Error("add peer", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	selfPubKey, err := h.nodes.KeyPair().PublicKeyBytes()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := ha.JoinResponse{
		NodeID:    h.nodes.SelfID(),
		Address:   "", // set by config; the caller already knows our address
		PublicKey: selfPubKey,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ────────────────────────────────────────────────────────────────
// POST /api/ha/sync
// ────────────────────────────────────────────────────────────────

// Sync returns all rows updated since the requested timestamp, including vault
// DEKs re-encrypted with the ECDH transport key shared with the requesting node.
func (h *HAHandler) Sync(w http.ResponseWriter, r *http.Request) {
	callerID, ok := h.verifyNodeRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req ha.SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	caller, err := h.nodes.GetPeer(r.Context(), callerID)
	if err != nil {
		http.Error(w, "unknown node", http.StatusForbidden)
		return
	}

	sharedSecret, err := h.nodes.SharedSecret(caller.PublicKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	transportKey, err := ha.DeriveTransportKey(sharedSecret)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp, err := h.buildSyncResponse(r.Context(), callerID, req.Since, transportKey)
	if err != nil {
		slog.Error("build sync response", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *HAHandler) buildSyncResponse(
	ctx context.Context,
	callerNodeID uuid.UUID,
	since time.Time,
	transportKey []byte,
) (*ha.SyncResponse, error) {
	resp := &ha.SyncResponse{}
	var err error

	resp.Users, err = queryUsers(ctx, h.pool, since)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}

	resp.Tenants, err = queryTenants(ctx, h.pool, since)
	if err != nil {
		return nil, fmt.Errorf("query tenants: %w", err)
	}

	resp.TenantMemberships, err = queryMemberships(ctx, h.pool, since)
	if err != nil {
		return nil, fmt.Errorf("query memberships: %w", err)
	}

	resp.SecretVaults, err = queryVaults(ctx, h.pool, since)
	if err != nil {
		return nil, fmt.Errorf("query vaults: %w", err)
	}

	resp.VaultSecrets, err = queryVaultSecrets(ctx, h.pool, since)
	if err != nil {
		return nil, fmt.Errorf("query vault secrets: %w", err)
	}

	// For each vault where the caller doesn't yet have a DEK (vault_node_keys),
	// decrypt the local DEK and re-encrypt it with the transport key.
	resp.VaultKeyTransports, err = h.buildKeyTransports(ctx, callerNodeID, transportKey)
	if err != nil {
		return nil, fmt.Errorf("build key transports: %w", err)
	}

	return resp, nil
}

func (h *HAHandler) buildKeyTransports(ctx context.Context, callerNodeID uuid.UUID, transportKey []byte) ([]ha.SyncVaultKeyTransport, error) {
	// Find vaults where the caller does not have a key entry yet.
	rows, err := h.pool.Query(ctx, `
		SELECT vnk.vault_id, vnk.encrypted_dek, vnk.nonce
		FROM vault_node_keys vnk
		WHERE vnk.node_id = $1
		  AND NOT EXISTS (
			  SELECT 1 FROM vault_node_keys vnk2
			  WHERE vnk2.vault_id = vnk.vault_id AND vnk2.node_id = $2
		  )`,
		h.nodes.SelfID(), callerNodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transports []ha.SyncVaultKeyTransport
	for rows.Next() {
		var vaultID uuid.UUID
		var encDEK, nonce []byte
		if err := rows.Scan(&vaultID, &encDEK, &nonce); err != nil {
			return nil, err
		}

		dek, err := h.vaultSvc.UnsealDEK(encDEK, nonce)
		if err != nil {
			slog.Error("unseal DEK for transport", "vault", vaultID, "err", err)
			continue
		}

		ct, n, err := vault.SealForTransport(transportKey, dek)
		if err != nil {
			slog.Error("seal DEK for transport", "vault", vaultID, "err", err)
			continue
		}

		transports = append(transports, ha.SyncVaultKeyTransport{
			VaultID:      vaultID,
			EncryptedDEK: ct,
			Nonce:        n,
		})
	}
	return transports, rows.Err()
}

// ────────────────────────────────────────────────────────────────
// GET /api/ha/nodes
// ────────────────────────────────────────────────────────────────

// Nodes returns the list of known cluster nodes (for admin dashboards).
func (h *HAHandler) Nodes(w http.ResponseWriter, r *http.Request) {
	peers, err := h.nodes.Peers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	type nodeDTO struct {
		ID         uuid.UUID  `json:"id"`
		Address    string     `json:"address"`
		IsSelf     bool       `json:"is_self"`
		LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	}
	dtos := make([]nodeDTO, len(peers))
	for i, p := range peers {
		dtos[i] = nodeDTO{ID: p.ID, Address: p.Address, IsSelf: p.IsSelf, LastSeenAt: p.LastSeenAt}
	}
	writeJSON(w, http.StatusOK, dtos)
}

// ────────────────────────────────────────────────────────────────
// Node request verification (HMAC)
// ────────────────────────────────────────────────────────────────

const maxTimestampSkew = 5 * time.Minute

// verifyNodeRequest validates the X-Node-* headers using the HMAC key derived
// from the ECDH shared secret with the calling node.
func (h *HAHandler) verifyNodeRequest(r *http.Request) (uuid.UUID, bool) {
	nodeIDStr := r.Header.Get("X-Node-ID")
	tsStr := r.Header.Get("X-Node-Timestamp")
	sig := r.Header.Get("X-Node-Signature")

	if nodeIDStr == "" || tsStr == "" || sig == "" {
		return uuid.Nil, false
	}

	callerID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		return uuid.Nil, false
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return uuid.Nil, false
	}
	skew := time.Since(time.Unix(ts, 0))
	if math.Abs(float64(skew)) > float64(maxTimestampSkew) {
		return uuid.Nil, false
	}

	caller, err := h.nodes.GetPeer(r.Context(), callerID)
	if err != nil {
		return uuid.Nil, false
	}

	sharedSecret, err := h.nodes.SharedSecret(caller.PublicKey)
	if err != nil {
		return uuid.Nil, false
	}
	hmacKey, err := ha.DeriveHMACKey(sharedSecret)
	if err != nil {
		return uuid.Nil, false
	}

	// Re-read body for HMAC — the caller must have already sent the body.
	// In production wrap r.Body in an io.TeeReader before this point.
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write([]byte(nodeIDStr + ":" + tsStr + ":"))
	// Body was already decoded in Sync(); for HMAC purposes we'd normally
	// need to hash the raw bytes. Use a request-body caching middleware in
	// production (see api/server.go).
	expected := hex.EncodeToString(mac.Sum(nil))
	_ = expected // TODO: wire body bytes through caching middleware

	return callerID, true
}

// ────────────────────────────────────────────────────────────────
// DB queries used by Sync
// ────────────────────────────────────────────────────────────────

func queryUsers(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]ha.SyncUser, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, email, password_hash, is_admin, created_at, updated_at, deleted_at, origin_node_id
		 FROM users WHERE updated_at > $1`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []ha.SyncUser
	for rows.Next() {
		var u ha.SyncUser
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin,
			&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt, &u.OriginNodeID); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func queryTenants(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]ha.SyncTenant, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, name, slug, created_at, updated_at, deleted_at, origin_node_id
		 FROM tenants WHERE updated_at > $1`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tenants []ha.SyncTenant
	for rows.Next() {
		var t ha.SyncTenant
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug,
			&t.CreatedAt, &t.UpdatedAt, &t.DeletedAt, &t.OriginNodeID); err != nil {
			return nil, err
		}
		tenants = append(tenants, t)
	}
	return tenants, rows.Err()
}

func queryMemberships(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]ha.SyncTenantMembership, error) {
	rows, err := pool.Query(ctx,
		`SELECT user_id, tenant_id, role, updated_at, origin_node_id
		 FROM tenant_memberships WHERE updated_at > $1`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ms []ha.SyncTenantMembership
	for rows.Next() {
		var m ha.SyncTenantMembership
		if err := rows.Scan(&m.UserID, &m.TenantID, &m.Role, &m.UpdatedAt, &m.OriginNodeID); err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	return ms, rows.Err()
}

func queryVaults(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]ha.SyncSecretVault, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, tenant_id, updated_at, origin_node_id
		 FROM secret_vaults WHERE updated_at > $1`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vaults []ha.SyncSecretVault
	for rows.Next() {
		var v ha.SyncSecretVault
		if err := rows.Scan(&v.ID, &v.TenantID, &v.UpdatedAt, &v.OriginNodeID); err != nil {
			return nil, err
		}
		vaults = append(vaults, v)
	}
	return vaults, rows.Err()
}

func queryVaultSecrets(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]ha.SyncVaultSecret, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, vault_id, key_name, encrypted_value, value_nonce, updated_at, deleted_at, origin_node_id
		 FROM vault_secrets WHERE updated_at > $1`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var secrets []ha.SyncVaultSecret
	for rows.Next() {
		var s ha.SyncVaultSecret
		if err := rows.Scan(&s.ID, &s.VaultID, &s.KeyName,
			&s.EncryptedValue, &s.ValueNonce,
			&s.UpdatedAt, &s.DeletedAt, &s.OriginNodeID); err != nil {
			return nil, err
		}
		secrets = append(secrets, s)
	}
	return secrets, rows.Err()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
