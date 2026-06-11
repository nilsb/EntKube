package vault

import (
	"context"
	"fmt"

	"github.com/entkube/entkube/internal/ha"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncService implements ha.VaultSyncer. It is separate from Service so it can
// hold a reference to the NodeManager without creating an import cycle.
type SyncService struct {
	pool     *pgxpool.Pool
	vaultSvc *Service
	selfNode uuid.UUID
	// getSharedSecret is provided by the NodeManager; injected at wire-up time.
	getSharedSecret func(peerPublicKey []byte) ([]byte, error)
}

// NewSyncService creates a VaultSyncService.
func NewSyncService(
	pool *pgxpool.Pool,
	vaultSvc *Service,
	selfNode uuid.UUID,
	getSharedSecret func(peerPublicKey []byte) ([]byte, error),
) *SyncService {
	return &SyncService{
		pool:            pool,
		vaultSvc:        vaultSvc,
		selfNode:        selfNode,
		getSharedSecret: getSharedSecret,
	}
}

// ApplyVaultKeyTransport decrypts a DEK received from a peer over the ECDH
// transport channel and stores it sealed with this node's own root key.
func (s *SyncService) ApplyVaultKeyTransport(ctx context.Context, peerNodeID uuid.UUID, kt ha.SyncVaultKeyTransport) error {
	var peerPubKey []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT public_key FROM ha_nodes WHERE id = $1`, peerNodeID).Scan(&peerPubKey); err != nil {
		return fmt.Errorf("get peer public key for %s: %w", peerNodeID, err)
	}

	sharedSecret, err := s.getSharedSecret(peerPubKey)
	if err != nil {
		return fmt.Errorf("derive shared secret: %w", err)
	}

	dek, err := UnsealFromTransport(sharedSecret, kt.Nonce, kt.EncryptedDEK)
	if err != nil {
		return fmt.Errorf("unseal transport DEK for vault %s: %w", kt.VaultID, err)
	}

	encDEK, nonce, err := s.vaultSvc.SealDEK(dek)
	if err != nil {
		return fmt.Errorf("re-seal DEK: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO vault_node_keys (vault_id, node_id, encrypted_dek, nonce)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (vault_id, node_id) DO NOTHING`,
		kt.VaultID, s.selfNode, encDEK, nonce)
	return err
}
