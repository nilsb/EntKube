-- +goose Up
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ────────────────────────────────────────────────────────────────
-- HA cluster node registry
-- Each running instance registers itself here. Public keys are
-- X25519 PKIX-encoded bytes used for ECDH vault-key transport.
-- ────────────────────────────────────────────────────────────────
CREATE TABLE ha_nodes (
    id           UUID        PRIMARY KEY,
    address      TEXT        NOT NULL,
    public_key   BYTEA       NOT NULL,
    is_self      BOOLEAN     NOT NULL DEFAULT FALSE,
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Enforce exactly one self row per database
CREATE UNIQUE INDEX ha_nodes_one_self ON ha_nodes (is_self) WHERE is_self = TRUE;

-- Per-peer sync cursors. Tracks the high-water-mark timestamp up to
-- which this node has successfully applied changes from a remote node.
CREATE TABLE ha_sync_cursors (
    local_node_id  UUID        NOT NULL REFERENCES ha_nodes(id) ON DELETE CASCADE,
    remote_node_id UUID        NOT NULL REFERENCES ha_nodes(id) ON DELETE CASCADE,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    PRIMARY KEY (local_node_id, remote_node_id)
);

-- Distributed leader-election leases. Background services (e.g.
-- AlertSync, DeploymentSync) acquire a named lease before running;
-- any node can take over when the lease expires.
CREATE TABLE ha_leases (
    service_name TEXT        PRIMARY KEY,
    node_id      UUID        NOT NULL REFERENCES ha_nodes(id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ NOT NULL,
    renewed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ────────────────────────────────────────────────────────────────
-- Auth / identity
-- ────────────────────────────────────────────────────────────────
CREATE TABLE users (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email          TEXT        UNIQUE NOT NULL,
    password_hash  TEXT        NOT NULL,
    is_admin       BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id)
);

-- Long-lived refresh tokens stored hashed (SHA-256).
-- Access tokens are short-lived JWTs issued in-memory.
CREATE TABLE refresh_tokens (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash     TEXT        NOT NULL UNIQUE,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at     TIMESTAMPTZ,
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id)
);

-- ────────────────────────────────────────────────────────────────
-- Multi-tenancy
-- ────────────────────────────────────────────────────────────────
CREATE TABLE tenants (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT        NOT NULL,
    slug           TEXT        UNIQUE NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id)
);

CREATE TABLE tenant_memberships (
    user_id        UUID        NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    tenant_id      UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    role           TEXT        NOT NULL DEFAULT 'member',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id),
    PRIMARY KEY (user_id, tenant_id)
);

-- ────────────────────────────────────────────────────────────────
-- Secret vault
-- Each tenant has one vault. The actual Data Encryption Key (DEK)
-- is stored once per (vault, node) pair — each node seals the DEK
-- with its own root key. Individual secret ciphertexts use the DEK
-- and are therefore identical across nodes; only the sealed DEK
-- differs.
-- ────────────────────────────────────────────────────────────────
CREATE TABLE secret_vaults (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID        UNIQUE NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    origin_node_id UUID        NOT NULL REFERENCES ha_nodes(id)
);

-- Per-node sealed DEK. encrypted_dek is the raw DEK encrypted with
-- AES-256-GCM using this node's root key. During sync the DEK is
-- transported re-encrypted under an ECDH-derived transport key.
CREATE TABLE vault_node_keys (
    vault_id      UUID  NOT NULL REFERENCES secret_vaults(id) ON DELETE CASCADE,
    node_id       UUID  NOT NULL REFERENCES ha_nodes(id)      ON DELETE CASCADE,
    encrypted_dek BYTEA NOT NULL,
    nonce         BYTEA NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (vault_id, node_id)
);

-- Individual secrets — value encrypted with the vault's DEK.
CREATE TABLE vault_secrets (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id        UUID        NOT NULL REFERENCES secret_vaults(id) ON DELETE CASCADE,
    key_name        TEXT        NOT NULL,
    encrypted_value BYTEA       NOT NULL,
    value_nonce     BYTEA       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    origin_node_id  UUID        NOT NULL REFERENCES ha_nodes(id),
    UNIQUE (vault_id, key_name)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vault_secrets;
DROP TABLE IF EXISTS vault_node_keys;
DROP TABLE IF EXISTS secret_vaults;
DROP TABLE IF EXISTS tenant_memberships;
DROP TABLE IF EXISTS tenants;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS ha_leases;
DROP TABLE IF EXISTS ha_sync_cursors;
DROP TABLE IF EXISTS ha_nodes;
DROP EXTENSION IF EXISTS "pgcrypto";
-- +goose StatementEnd
