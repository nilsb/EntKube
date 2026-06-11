// Package ha implements High Availability: node registration, ECDH-based
// inter-node authentication, incremental data sync, and leader-election leases.
package ha

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/hkdf"
)

// KeyPair is an X25519 key pair used for ECDH vault-DEK transport and
// request-level HMAC authentication between nodes.
type KeyPair struct {
	priv *ecdh.PrivateKey
}

// GenerateKeyPair creates a fresh X25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate X25519 key: %w", err)
	}
	return &KeyPair{priv: priv}, nil
}

// LoadOrGenerateKeyPair reads the key pair from a PEM file, generating and
// saving a new one if the file does not exist.
func LoadOrGenerateKeyPair(path string) (*KeyPair, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		kp, err := GenerateKeyPair()
		if err != nil {
			return nil, err
		}
		if err := kp.SavePEM(path); err != nil {
			return nil, fmt.Errorf("save node key: %w", err)
		}
		return kp, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", path, err)
	}
	return parsePEM(data)
}

// PublicKeyBytes returns the PKIX-encoded public key bytes for storage in the DB.
func (kp *KeyPair) PublicKeyBytes() ([]byte, error) {
	return x509.MarshalPKIXPublicKey(kp.priv.PublicKey())
}

// SharedSecret computes the raw X25519 shared secret with a peer public key.
// Always pass the result through DeriveTransportKey or DeriveHMACKey before use.
func (kp *KeyPair) SharedSecret(peerPKIX []byte) ([]byte, error) {
	pub, err := parsePublicKey(peerPKIX)
	if err != nil {
		return nil, err
	}
	secret, err := kp.priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("ECDH: %w", err)
	}
	return secret, nil
}

// DeriveTransportKey derives a 32-byte AES-256 key for vault DEK transport
// from a raw ECDH shared secret.
func DeriveTransportKey(sharedSecret []byte) ([]byte, error) {
	return hkdfDerive(sharedSecret, "entkube-vault-transport-v1")
}

// DeriveHMACKey derives a 32-byte key for HMAC request authentication between
// nodes from a raw ECDH shared secret.
func DeriveHMACKey(sharedSecret []byte) ([]byte, error) {
	return hkdfDerive(sharedSecret, "entkube-node-hmac-v1")
}

func hkdfDerive(secret []byte, info string) ([]byte, error) {
	kdf := hkdf.New(sha256.New, secret, nil, []byte(info))
	key := make([]byte, 32)
	if _, err := io.ReadFull(kdf, key); err != nil {
		return nil, fmt.Errorf("hkdf derive (%s): %w", info, err)
	}
	return key, nil
}

// SavePEM writes the private key to a PEM file with mode 0600.
func (kp *KeyPair) SavePEM(path string) error {
	der, err := x509.MarshalPKCS8PrivateKey(kp.priv)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0600)
}

func parsePEM(data []byte) (*KeyPair, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in key file")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS8 key: %w", err)
	}
	ecdhKey, ok := key.(*ecdh.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key file does not contain an ECDH private key")
	}
	return &KeyPair{priv: ecdhKey}, nil
}

func parsePublicKey(pkix []byte) (*ecdh.PublicKey, error) {
	pub, err := x509.ParsePKIXPublicKey(pkix)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX public key: %w", err)
	}
	ecdhPub, ok := pub.(*ecdh.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not an ECDH key")
	}
	return ecdhPub, nil
}
