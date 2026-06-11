package ha

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ────────────────────────────────────────────────────────────────
// Wire types shared between sync client and server
// ────────────────────────────────────────────────────────────────

// SyncRequest is the payload this node sends when pulling changes from a peer.
type SyncRequest struct {
	Since time.Time `json:"since"`
}

// SyncResponse contains all rows the peer updated after Since.
type SyncResponse struct {
	Users              []SyncUser              `json:"users,omitempty"`
	Tenants            []SyncTenant            `json:"tenants,omitempty"`
	TenantMemberships  []SyncTenantMembership  `json:"tenant_memberships,omitempty"`
	SecretVaults       []SyncSecretVault       `json:"secret_vaults,omitempty"`
	VaultSecrets       []SyncVaultSecret       `json:"vault_secrets,omitempty"`
	VaultKeyTransports []SyncVaultKeyTransport  `json:"vault_key_transports,omitempty"`
}

type SyncUser struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"password_hash"`
	IsAdmin      bool       `json:"is_admin"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	OriginNodeID uuid.UUID  `json:"origin_node_id"`
}

type SyncTenant struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Slug         string     `json:"slug"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	OriginNodeID uuid.UUID  `json:"origin_node_id"`
}

type SyncTenantMembership struct {
	UserID       uuid.UUID `json:"user_id"`
	TenantID     uuid.UUID `json:"tenant_id"`
	Role         string    `json:"role"`
	UpdatedAt    time.Time `json:"updated_at"`
	OriginNodeID uuid.UUID `json:"origin_node_id"`
}

type SyncSecretVault struct {
	ID           uuid.UUID `json:"id"`
	TenantID     uuid.UUID `json:"tenant_id"`
	UpdatedAt    time.Time `json:"updated_at"`
	OriginNodeID uuid.UUID `json:"origin_node_id"`
}

type SyncVaultSecret struct {
	ID             uuid.UUID  `json:"id"`
	VaultID        uuid.UUID  `json:"vault_id"`
	KeyName        string     `json:"key_name"`
	EncryptedValue []byte     `json:"encrypted_value"` // encrypted with vault DEK — safe to replicate as-is
	ValueNonce     []byte     `json:"value_nonce"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	OriginNodeID   uuid.UUID  `json:"origin_node_id"`
}

// SyncVaultKeyTransport carries a vault DEK encrypted with the ECDH transport
// key shared between the two nodes. The receiver decrypts it with the shared
// secret and re-seals it with its own root key.
type SyncVaultKeyTransport struct {
	VaultID      uuid.UUID `json:"vault_id"`
	EncryptedDEK []byte    `json:"encrypted_dek"`
	Nonce        []byte    `json:"nonce"`
}

// JoinRequest is sent by a new node to an existing node to join the cluster.
type JoinRequest struct {
	NodeID    uuid.UUID `json:"node_id"`
	Address   string    `json:"address"`
	PublicKey []byte    `json:"public_key"` // PKIX-encoded X25519
	JoinToken string    `json:"join_token"`
}

// JoinResponse is the existing node's reply, containing its own identity.
type JoinResponse struct {
	NodeID    uuid.UUID `json:"node_id"`
	Address   string    `json:"address"`
	PublicKey []byte    `json:"public_key"`
}

// ────────────────────────────────────────────────────────────────
// Sync manager (runs on each node, pulls from all peers)
// ────────────────────────────────────────────────────────────────

// SyncManager periodically pulls changes from each known peer and applies them.
type SyncManager struct {
	pool        *pgxpool.Pool
	nodes       *NodeManager
	vaultSync   VaultSyncer
	httpClient  *http.Client
	interval    time.Duration
}

// VaultSyncer is implemented by the vault service to handle DEK transport.
type VaultSyncer interface {
	// ApplyVaultKeyTransport decrypts an incoming DEK (encrypted with the
	// shared transport key) and stores it sealed with this node's root key.
	ApplyVaultKeyTransport(ctx context.Context, nodeID uuid.UUID, kt SyncVaultKeyTransport) error
}

// NewSyncManager creates a SyncManager that syncs every interval.
func NewSyncManager(pool *pgxpool.Pool, nodes *NodeManager, vs VaultSyncer, interval time.Duration) *SyncManager {
	return &SyncManager{
		pool:       pool,
		nodes:      nodes,
		vaultSync:  vs,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		interval:   interval,
	}
}

// Run starts the sync loop. Call in a goroutine; returns when ctx is cancelled.
func (sm *SyncManager) Run(ctx context.Context) {
	ticker := time.NewTicker(sm.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sm.syncAll(ctx)
		}
	}
}

func (sm *SyncManager) syncAll(ctx context.Context) {
	peers, err := sm.nodes.Peers(ctx)
	if err != nil {
		slog.Error("list peers for sync", "err", err)
		return
	}
	for _, peer := range peers {
		if err := sm.syncFromPeer(ctx, peer); err != nil {
			slog.Error("sync from peer failed", "peer", peer.ID, "err", err)
		}
	}
}

func (sm *SyncManager) syncFromPeer(ctx context.Context, peer Node) error {
	cursor, err := sm.getCursor(ctx, peer.ID)
	if err != nil {
		return fmt.Errorf("get cursor: %w", err)
	}

	sharedSecret, err := sm.nodes.SharedSecret(peer.PublicKey)
	if err != nil {
		return fmt.Errorf("derive shared secret: %w", err)
	}
	hmacKey, err := DeriveHMACKey(sharedSecret)
	if err != nil {
		return fmt.Errorf("derive hmac key: %w", err)
	}

	body, _ := json.Marshal(SyncRequest{Since: cursor})
	resp, err := sm.signedPost(ctx, peer, "/api/ha/sync", body, hmacKey)
	if err != nil {
		return fmt.Errorf("sync request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned %d", resp.StatusCode)
	}

	var sr SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return fmt.Errorf("decode sync response: %w", err)
	}

	now := time.Now()
	if err := sm.apply(ctx, peer, sharedSecret, sr); err != nil {
		return fmt.Errorf("apply sync: %w", err)
	}
	return sm.updateCursor(ctx, peer.ID, now)
}

// apply writes incoming rows into the local database using last-write-wins on updated_at.
func (sm *SyncManager) apply(ctx context.Context, peer Node, sharedSecret []byte, sr SyncResponse) error {
	tx, err := sm.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, u := range sr.Users {
		_, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, is_admin, created_at, updated_at, deleted_at, origin_node_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (id) DO UPDATE SET
				email         = EXCLUDED.email,
				password_hash = EXCLUDED.password_hash,
				is_admin      = EXCLUDED.is_admin,
				updated_at    = EXCLUDED.updated_at,
				deleted_at    = EXCLUDED.deleted_at
			WHERE EXCLUDED.updated_at > users.updated_at`,
			u.ID, u.Email, u.PasswordHash, u.IsAdmin,
			u.CreatedAt, u.UpdatedAt, u.DeletedAt, u.OriginNodeID)
		if err != nil {
			return fmt.Errorf("upsert user %s: %w", u.ID, err)
		}
	}

	for _, t := range sr.Tenants {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenants (id, name, slug, created_at, updated_at, deleted_at, origin_node_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (id) DO UPDATE SET
				name       = EXCLUDED.name,
				slug       = EXCLUDED.slug,
				updated_at = EXCLUDED.updated_at,
				deleted_at = EXCLUDED.deleted_at
			WHERE EXCLUDED.updated_at > tenants.updated_at`,
			t.ID, t.Name, t.Slug, t.CreatedAt, t.UpdatedAt, t.DeletedAt, t.OriginNodeID)
		if err != nil {
			return fmt.Errorf("upsert tenant %s: %w", t.ID, err)
		}
	}

	for _, m := range sr.TenantMemberships {
		_, err := tx.Exec(ctx, `
			INSERT INTO tenant_memberships (user_id, tenant_id, role, created_at, updated_at, origin_node_id)
			VALUES ($1,$2,$3,now(),$4,$5)
			ON CONFLICT (user_id, tenant_id) DO UPDATE SET
				role       = EXCLUDED.role,
				updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > tenant_memberships.updated_at`,
			m.UserID, m.TenantID, m.Role, m.UpdatedAt, m.OriginNodeID)
		if err != nil {
			return fmt.Errorf("upsert membership: %w", err)
		}
	}

	for _, v := range sr.SecretVaults {
		_, err := tx.Exec(ctx, `
			INSERT INTO secret_vaults (id, tenant_id, created_at, updated_at, origin_node_id)
			VALUES ($1,$2,now(),$3,$4)
			ON CONFLICT (id) DO NOTHING`,
			v.ID, v.TenantID, v.UpdatedAt, v.OriginNodeID)
		if err != nil {
			return fmt.Errorf("upsert vault %s: %w", v.ID, err)
		}
	}

	for _, s := range sr.VaultSecrets {
		_, err := tx.Exec(ctx, `
			INSERT INTO vault_secrets
				(id, vault_id, key_name, encrypted_value, value_nonce, created_at, updated_at, deleted_at, origin_node_id)
			VALUES ($1,$2,$3,$4,$5,now(),$6,$7,$8)
			ON CONFLICT (vault_id, key_name) DO UPDATE SET
				encrypted_value = EXCLUDED.encrypted_value,
				value_nonce     = EXCLUDED.value_nonce,
				updated_at      = EXCLUDED.updated_at,
				deleted_at      = EXCLUDED.deleted_at
			WHERE EXCLUDED.updated_at > vault_secrets.updated_at`,
			s.ID, s.VaultID, s.KeyName, s.EncryptedValue, s.ValueNonce,
			s.UpdatedAt, s.DeletedAt, s.OriginNodeID)
		if err != nil {
			return fmt.Errorf("upsert vault secret: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Vault DEKs are applied outside the main transaction because they require
	// root-key crypto that happens in the vault service layer.
	for _, kt := range sr.VaultKeyTransports {
		if err := sm.vaultSync.ApplyVaultKeyTransport(ctx, peer.ID, kt); err != nil {
			slog.Error("apply vault key transport", "vault", kt.VaultID, "err", err)
		}
	}
	return nil
}

// signedPost sends a POST request to a peer, including an HMAC-SHA256 signature
// over (nodeID:timestamp:bodyHex) so the peer can authenticate the request.
func (sm *SyncManager) signedPost(ctx context.Context, peer Node, path string, body, hmacKey []byte) (*http.Response, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := computeHMAC(hmacKey, sm.nodes.SelfID().String(), ts, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.Address+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", sm.nodes.SelfID().String())
	req.Header.Set("X-Node-Timestamp", ts)
	req.Header.Set("X-Node-Signature", sig)

	return sm.httpClient.Do(req)
}

func computeHMAC(key []byte, nodeID, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(nodeID + ":" + timestamp + ":"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ────────────────────────────────────────────────────────────────
// Sync cursor helpers
// ────────────────────────────────────────────────────────────────

func (sm *SyncManager) getCursor(ctx context.Context, peerID uuid.UUID) (time.Time, error) {
	var t time.Time
	err := sm.pool.QueryRow(ctx,
		`SELECT last_synced_at FROM ha_sync_cursors
		 WHERE local_node_id = $1 AND remote_node_id = $2`,
		sm.nodes.SelfID(), peerID).Scan(&t)
	if err != nil {
		// No cursor yet — sync everything from the epoch.
		return time.Time{}, nil
	}
	return t, nil
}

func (sm *SyncManager) updateCursor(ctx context.Context, peerID uuid.UUID, t time.Time) error {
	_, err := sm.pool.Exec(ctx, `
		INSERT INTO ha_sync_cursors (local_node_id, remote_node_id, last_synced_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (local_node_id, remote_node_id) DO UPDATE SET last_synced_at = EXCLUDED.last_synced_at`,
		sm.nodes.SelfID(), peerID, t)
	return err
}
