// Package auth implements JWT issuing/parsing with a revocation blacklist,
// plus Gin middleware for authentication and role checks.
package auth

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned when a token fails signature or format checks.
var ErrInvalidToken = errors.New("invalid token")

// ErrTokenExpired is returned when a token is past its expiry.
var ErrTokenExpired = errors.New("token expired")

// ErrTokenRevoked is returned when a token was invalidated by logout.
var ErrTokenRevoked = errors.New("token revoked")

// Claims is the JWT payload.
type Claims struct {
	UserID   string `json:"uid"`
	Username string `json:"username"`
	Role     string `json:"role"`
	// Purpose is "access" (default, normal routes) or "mfa_challenge"
	// (short-lived token returned by login when the user has MFA enabled, only
	// usable at /auth/mfa/verify).
	Purpose string `json:"purpose,omitempty"`
	jwt.RegisteredClaims
}

// Purposes a token can carry.
const (
	PurposeAccess       = "access"
	PurposeMFAChallenge = "mfa_challenge"
)

// Manager issues and validates JWTs and keeps a blacklist of revoked tokens.
type Manager struct {
	secret          []byte
	ttl             time.Duration // access token lifetime
	mfaChallengeTTL time.Duration // second-factor challenge token lifetime
	issuer          string
	blacklist       *Blacklist
	now             func() time.Time // injectable for tests
}

// MFAChallengeTTL is the lifetime of a login step-2 challenge token.
const MFAChallengeTTL = 5 * time.Minute

// NewManager creates a JWT manager. An empty secret falls back to a generated
// one so the service still runs, but tokens do not survive restarts.
func NewManager(secret string, ttl time.Duration, issuer string) *Manager {
	key := []byte(secret)
	if len(key) == 0 {
		key = []byte(fmt.Sprintf("dev-only-%d", time.Now().UnixNano()))
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if issuer == "" {
		issuer = "wechatapp-web"
	}
	return &Manager{
		secret:          key,
		ttl:             ttl,
		mfaChallengeTTL: MFAChallengeTTL,
		issuer:          issuer,
		blacklist:       NewBlacklist(),
		now:             time.Now,
	}
}

// Issue creates a signed access token for the user.
func (m *Manager) Issue(userID, username, role string) (token string, expiresAt time.Time, err error) {
	return m.IssuePurpose(userID, username, role, PurposeAccess, m.ttl)
}

// IssueMFAChallenge creates a short-lived token that may only be used to
// complete the second factor at /auth/mfa/verify.
func (m *Manager) IssueMFAChallenge(userID, username, role string) (token string, expiresAt time.Time, err error) {
	return m.IssuePurpose(userID, username, role, PurposeMFAChallenge, m.mfaChallengeTTL)
}

// IssuePurpose creates a signed token with an explicit purpose and lifetime.
func (m *Manager) IssuePurpose(userID, username, role, purpose string, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	now := m.now()
	exp := now.Add(ttl)
	claims := Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		Purpose:  purpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        fmt.Sprintf("%s-%d", userID, now.UnixNano()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// Parse validates the token signature, expiry and blacklist and returns the
// claims.
func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}
	if !tok.Valid {
		return nil, ErrInvalidToken
	}
	if m.blacklist.Contains(claims.ID) {
		return nil, ErrTokenRevoked
	}
	return claims, nil
}

// Revoke invalidates the token identified by its claims (used by logout).
func (m *Manager) Revoke(claims *Claims) {
	if claims == nil {
		return
	}
	m.blacklist.Add(claims.ID, claims.ExpiresAt.Time)
}

// Blacklist tracks revoked token IDs until their expiry.
type Blacklist struct {
	mu    sync.Mutex
	items map[string]time.Time // jti -> expiry
}

// NewBlacklist creates an empty blacklist.
func NewBlacklist() *Blacklist {
	return &Blacklist{items: make(map[string]time.Time)}
}

// Add marks a token ID as revoked, dropping any expired entries.
func (b *Blacklist) Add(jti string, exp time.Time) {
	if jti == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for id, e := range b.items {
		if e.Before(now) {
			delete(b.items, id)
		}
	}
	b.items[jti] = exp
}

// Contains reports whether the token ID is currently revoked.
func (b *Blacklist) Contains(jti string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for id, e := range b.items {
		if e.Before(now) {
			delete(b.items, id)
		}
	}
	_, ok := b.items[jti]
	return ok
}
