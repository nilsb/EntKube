// Package auth handles user authentication via bcrypt passwords,
// short-lived JWT access tokens, and long-lived refresh tokens stored in the DB.
//
// Session model (stateless across nodes):
//   - Access token:  HS256 JWT, 15-minute TTL, signed with shared JWT_SECRET.
//     Every node can verify it without a DB round-trip.
//   - Refresh token: 64-byte random secret, stored SHA-256-hashed in the DB.
//     Exchanged for a new access token. Revocation is instant (delete the row).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 7 * 24 * time.Hour
	bcryptCost      = 12
)

// Claims is the JWT payload.
type Claims struct {
	jwt.RegisteredClaims
	UserID  uuid.UUID `json:"uid"`
	IsAdmin bool      `json:"adm"`
}

// TokenPair is returned on successful login or token refresh.
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Service performs all authentication operations.
type Service struct {
	pool      *pgxpool.Pool
	jwtSecret []byte
	selfNode  uuid.UUID
}

// New creates an auth Service.
func New(pool *pgxpool.Pool, jwtSecret []byte, selfNode uuid.UUID) *Service {
	return &Service{pool: pool, jwtSecret: jwtSecret, selfNode: selfNode}
}

// HashPassword returns a bcrypt hash of the plaintext password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

// Login verifies credentials and issues a TokenPair.
func (s *Service) Login(ctx context.Context, email, password string) (*TokenPair, error) {
	var userID uuid.UUID
	var hash string
	var isAdmin bool
	err := s.pool.QueryRow(ctx,
		`SELECT id, password_hash, is_admin FROM users
		 WHERE email = $1 AND deleted_at IS NULL`, email).
		Scan(&userID, &hash, &isAdmin)
	if err != nil {
		// Return a generic error; don't reveal whether the user exists.
		return nil, fmt.Errorf("invalid credentials")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	return s.issueTokenPair(ctx, userID, isAdmin)
}

// Refresh validates a refresh token and issues a new TokenPair.
// The old refresh token is revoked atomically.
func (s *Service) Refresh(ctx context.Context, rawRefreshToken string) (*TokenPair, error) {
	hash := hashToken(rawRefreshToken)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var rtID, userID uuid.UUID
	var isAdmin bool
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT rt.id, u.id, u.is_admin, rt.expires_at
		FROM refresh_tokens rt
		JOIN users u ON u.id = rt.user_id
		WHERE rt.token_hash = $1
		  AND rt.revoked_at IS NULL
		  AND rt.expires_at > now()
		  AND u.deleted_at IS NULL`, hash).
		Scan(&rtID, &userID, &isAdmin, &expiresAt)
	if err != nil {
		return nil, fmt.Errorf("invalid or expired refresh token")
	}

	// Revoke the consumed token (rotation).
	_, err = tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1`, rtID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return s.issueTokenPair(ctx, userID, isAdmin)
}

// Logout revokes a specific refresh token.
func (s *Service) Logout(ctx context.Context, rawRefreshToken string) error {
	hash := hashToken(rawRefreshToken)
	_, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		 WHERE token_hash = $1 AND revoked_at IS NULL`, hash)
	return err
}

// ValidateAccessToken parses and verifies a JWT access token.
func (s *Service) ValidateAccessToken(raw string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(raw, &Claims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return s.jwtSecret, nil
		},
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("invalid access token: %w", err)
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok {
		return nil, fmt.Errorf("malformed claims")
	}
	return claims, nil
}

// ────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────

func (s *Service) issueTokenPair(ctx context.Context, userID uuid.UUID, isAdmin bool) (*TokenPair, error) {
	expiry := time.Now().Add(accessTokenTTL)
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiry),
		},
		UserID:  userID,
		IsAdmin: isAdmin,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := tok.SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	rawRefresh, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}
	hash := hashToken(rawRefresh)
	_, err = s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, origin_node_id)
		VALUES ($1, $2, $3, $4)`,
		userID, hash, time.Now().Add(refreshTokenTTL), s.selfNode)
	if err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresAt:    expiry,
	}, nil
}

func generateRefreshToken() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
