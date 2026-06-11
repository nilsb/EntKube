// Package vault implements envelope encryption for tenant secrets.
//
// Encryption hierarchy:
//
//	Root key (per-node, from VAULT_ROOT_KEY env)
//	  └─ Data Encryption Key (DEK, per tenant vault)
//	       └─ Individual secret values
//
// The DEK is sealed (encrypted) by the root key using AES-256-GCM and stored
// in vault_node_keys. Because each node has a different root key the sealed DEK
// differs per node, but the underlying secret ciphertexts are identical across
// the cluster — only the DEK changes during inter-node vault key transport.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Service holds the node's root key and performs all vault crypto operations.
type Service struct {
	rootKey []byte // 32-byte AES-256 key
}

// New creates a VaultService from a 32-byte root key.
func New(rootKey []byte) (*Service, error) {
	if len(rootKey) != 32 {
		return nil, fmt.Errorf("vault root key must be 32 bytes, got %d", len(rootKey))
	}
	k := make([]byte, 32)
	copy(k, rootKey)
	return &Service{rootKey: k}, nil
}

// GenerateDEK generates a fresh 32-byte random Data Encryption Key.
func (s *Service) GenerateDEK() ([]byte, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("generate DEK: %w", err)
	}
	return dek, nil
}

// SealDEK encrypts a DEK with the node's root key.
// Returns (ciphertext, nonce, error).
func (s *Service) SealDEK(dek []byte) (ciphertext, nonce []byte, err error) {
	return aesgcmEncrypt(s.rootKey, dek)
}

// UnsealDEK decrypts a DEK that was sealed by SealDEK.
func (s *Service) UnsealDEK(ciphertext, nonce []byte) ([]byte, error) {
	dek, err := aesgcmDecrypt(s.rootKey, nonce, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("unseal DEK: %w", err)
	}
	return dek, nil
}

// EncryptSecret encrypts a secret value with the provided DEK.
// Returns (ciphertext, nonce, error).
func (s *Service) EncryptSecret(dek, plaintext []byte) (ciphertext, nonce []byte, err error) {
	return aesgcmEncrypt(dek, plaintext)
}

// DecryptSecret decrypts a secret value with the provided DEK.
func (s *Service) DecryptSecret(dek, nonce, ciphertext []byte) ([]byte, error) {
	return aesgcmDecrypt(dek, nonce, ciphertext)
}

// ────────────────────────────────────────────────────────────────
// Transport encryption for inter-node vault key sync
// ────────────────────────────────────────────────────────────────

// SealForTransport encrypts a DEK with a transport key derived from an ECDH
// shared secret. Used when this node sends a vault DEK to a peer.
func SealForTransport(sharedSecret, dek []byte) (ciphertext, nonce []byte, err error) {
	tk, err := deriveTransportKey(sharedSecret)
	if err != nil {
		return nil, nil, err
	}
	return aesgcmEncrypt(tk, dek)
}

// UnsealFromTransport decrypts a DEK received from a peer over the transport
// channel. The peer sealed it with the same ECDH shared secret.
func UnsealFromTransport(sharedSecret, nonce, ciphertext []byte) ([]byte, error) {
	tk, err := deriveTransportKey(sharedSecret)
	if err != nil {
		return nil, err
	}
	dek, err := aesgcmDecrypt(tk, nonce, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("unseal from transport: %w", err)
	}
	return dek, nil
}

// deriveTransportKey derives a 32-byte AES key from a raw ECDH shared secret
// using HKDF-SHA256, so we never use the raw DH output as a symmetric key.
func deriveTransportKey(sharedSecret []byte) ([]byte, error) {
	kdf := hkdf.New(sha256.New, sharedSecret, nil, []byte("entkube-vault-transport-v1"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(kdf, key); err != nil {
		return nil, fmt.Errorf("derive transport key: %w", err)
	}
	return key, nil
}

// ────────────────────────────────────────────────────────────────
// Low-level AES-256-GCM helpers
// ────────────────────────────────────────────────────────────────

func aesgcmEncrypt(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("new GCM: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize()) // 12 bytes (NIST recommended)
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

func aesgcmDecrypt(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new GCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}
	return plaintext, nil
}
