package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL  string
	VaultRootKey []byte // 32-byte AES-256 key decoded from VAULT_ROOT_KEY env
	NodeKeyFile  string // path to X25519 private key file (generated on first boot)
	NodeAddress  string // this node's publicly reachable URL, e.g. https://node1.example.com
	JWTSecret    []byte // HS256 signing key decoded from JWT_SECRET env
	Port         int
	StaticDir    string // directory to serve SPA from (default: ./static)
	HAPeers      []string // optional comma-separated peer addresses for bootstrapping
	HAJoinToken  string   // one-time token a new node presents when calling /api/ha/join
	// Bootstrap: if set and no users exist, create an admin user on startup
	AdminEmail    string
	AdminPassword string
}

func Load() (*Config, error) {
	cfg := &Config{}

	cfg.DatabaseURL = requireEnv("DATABASE_URL")

	rootKeyB64 := requireEnv("VAULT_ROOT_KEY")
	key, err := base64.StdEncoding.DecodeString(rootKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decode VAULT_ROOT_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("VAULT_ROOT_KEY must decode to 32 bytes, got %d", len(key))
	}
	cfg.VaultRootKey = key

	jwtSecretB64 := requireEnv("JWT_SECRET")
	jwtKey, err := base64.StdEncoding.DecodeString(jwtSecretB64)
	if err != nil {
		return nil, fmt.Errorf("decode JWT_SECRET: %w", err)
	}
	if len(jwtKey) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must decode to at least 32 bytes")
	}
	cfg.JWTSecret = jwtKey

	cfg.NodeAddress = requireEnv("NODE_ADDRESS")

	cfg.NodeKeyFile = envOr("NODE_KEY_FILE", "/data/node_key.pem")

	portStr := envOr("PORT", "8080")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid PORT %q: %w", portStr, err)
	}
	cfg.Port = port

	if peers := os.Getenv("HA_PEERS"); peers != "" {
		for _, p := range strings.Split(peers, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.HAPeers = append(cfg.HAPeers, p)
			}
		}
	}

	cfg.HAJoinToken = os.Getenv("HA_JOIN_TOKEN")
	cfg.AdminEmail = os.Getenv("ADMIN_EMAIL")
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD")
	cfg.StaticDir = envOr("STATIC_DIR", "./static")

	return cfg, nil
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required env var %s is not set\n", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
