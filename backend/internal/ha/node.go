package ha

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Node represents a cluster member as stored in ha_nodes.
type Node struct {
	ID          uuid.UUID
	Address     string
	PublicKey   []byte // PKIX-encoded X25519 public key
	IsSelf      bool
	LastSeenAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NodeManager handles registration of this node and discovery of peers.
type NodeManager struct {
	pool    *pgxpool.Pool
	selfID  uuid.UUID
	keyPair *KeyPair
	address string
}

// NewNodeManager creates a NodeManager. It does not yet register the node;
// call Register to upsert this node into ha_nodes.
func NewNodeManager(pool *pgxpool.Pool, selfID uuid.UUID, kp *KeyPair, address string) *NodeManager {
	return &NodeManager{
		pool:    pool,
		selfID:  selfID,
		keyPair: kp,
		address: address,
	}
}

// SelfID returns this node's UUID.
func (m *NodeManager) SelfID() uuid.UUID { return m.selfID }

// SelfAddress returns this node's public address.
func (m *NodeManager) SelfAddress() string { return m.address }

// KeyPair returns this node's ECDH key pair.
func (m *NodeManager) KeyPair() *KeyPair { return m.keyPair }

// Register upserts this node into ha_nodes, marking it as is_self=true.
// All other is_self rows are cleared first (handles the unique constraint).
func (m *NodeManager) Register(ctx context.Context) error {
	pubKey, err := m.keyPair.PublicKeyBytes()
	if err != nil {
		return fmt.Errorf("get public key bytes: %w", err)
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Clear any stale self-flag from previous runs of this same node ID.
	_, err = tx.Exec(ctx,
		`UPDATE ha_nodes SET is_self = FALSE, updated_at = now() WHERE id = $1 AND is_self = TRUE`,
		m.selfID)
	if err != nil {
		return fmt.Errorf("clear stale self flag: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO ha_nodes (id, address, public_key, is_self, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, TRUE, now(), now(), now())
		ON CONFLICT (id) DO UPDATE SET
			address      = EXCLUDED.address,
			public_key   = EXCLUDED.public_key,
			is_self      = TRUE,
			last_seen_at = now(),
			updated_at   = now()`,
		m.selfID, m.address, pubKey)
	if err != nil {
		return fmt.Errorf("upsert self node: %w", err)
	}

	return tx.Commit(ctx)
}

// Heartbeat updates last_seen_at for this node so peers know it is alive.
func (m *NodeManager) Heartbeat(ctx context.Context) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE ha_nodes SET last_seen_at = now(), updated_at = now() WHERE id = $1`,
		m.selfID)
	return err
}

// Peers returns all non-self nodes from the database.
func (m *NodeManager) Peers(ctx context.Context) ([]Node, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT id, address, public_key, is_self, last_seen_at, created_at, updated_at
		 FROM ha_nodes WHERE id != $1 ORDER BY created_at`,
		m.selfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// AddPeer registers a peer node discovered during /api/ha/join handshake.
// No-ops if the peer already exists (uses ON CONFLICT).
func (m *NodeManager) AddPeer(ctx context.Context, peer Node) error {
	_, err := m.pool.Exec(ctx, `
		INSERT INTO ha_nodes (id, address, public_key, is_self, last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, FALSE, now(), now(), now())
		ON CONFLICT (id) DO UPDATE SET
			address      = EXCLUDED.address,
			public_key   = EXCLUDED.public_key,
			last_seen_at = now(),
			updated_at   = now()`,
		peer.ID, peer.Address, peer.PublicKey)
	return err
}

// GetPeer returns a single peer by ID.
func (m *NodeManager) GetPeer(ctx context.Context, id uuid.UUID) (*Node, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT id, address, public_key, is_self, last_seen_at, created_at, updated_at
		 FROM ha_nodes WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes, err := scanNodes(rows)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("node %s not found", id)
	}
	n := nodes[0]
	return &n, nil
}

// SharedSecret derives the raw ECDH shared secret with a peer.
func (m *NodeManager) SharedSecret(peerPublicKey []byte) ([]byte, error) {
	return m.keyPair.SharedSecret(peerPublicKey)
}

func scanNodes(rows pgx.Rows) ([]Node, error) {
	var nodes []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(
			&n.ID, &n.Address, &n.PublicKey, &n.IsSelf,
			&n.LastSeenAt, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}
