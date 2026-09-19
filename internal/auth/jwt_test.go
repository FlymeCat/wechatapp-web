package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIssueAndParse(t *testing.T) {
	m := NewManager("test-secret", time.Hour, "wechatapp-web")
	token, exp, err := m.Issue("uid-1", "alice", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if exp.Before(time.Now()) {
		t.Error("expiry in the past")
	}

	claims, err := m.Parse(token)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.UserID != "uid-1" || claims.Username != "alice" || claims.Role != "admin" {
		t.Errorf("claims = %+v", claims)
	}
	if claims.ID == "" {
		t.Error("missing jti")
	}
}

func TestParseWrongSecret(t *testing.T) {
	m1 := NewManager("secret-1", time.Hour, "wechatapp-web")
	m2 := NewManager("secret-2", time.Hour, "wechatapp-web")
	token, _, _ := m1.Issue("uid-1", "alice", "user")
	if _, err := m2.Parse(token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err = %v, want ErrInvalidToken", err)
	}
}

func TestParseTampered(t *testing.T) {
	m := NewManager("test-secret", time.Hour, "wechatapp-web")
	token, _, _ := m.Issue("uid-1", "alice", "user")
	if _, err := m.Parse(token + "x"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err = %v, want ErrInvalidToken", err)
	}
}

func TestParseExpired(t *testing.T) {
	m := NewManager("test-secret", time.Hour, "wechatapp-web")
	// Build a token whose ExpiresAt is already in the past.
	claims := Claims{
		UserID:   "uid-1",
		Username: "alice",
		Role:     "user",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "wechatapp-web",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(m.secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Parse(signed); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("err = %v, want ErrTokenExpired", err)
	}
}

func TestRevoke(t *testing.T) {
	m := NewManager("test-secret", time.Hour, "wechatapp-web")
	token, _, _ := m.Issue("uid-1", "alice", "user")

	claims, err := m.Parse(token)
	if err != nil {
		t.Fatal(err)
	}
	m.Revoke(claims)

	if _, err := m.Parse(token); !errors.Is(err, ErrTokenRevoked) {
		t.Errorf("err = %v, want ErrTokenRevoked", err)
	}
}

func TestBlacklistCleanup(t *testing.T) {
	b := NewBlacklist()
	b.Add("revoked-1", time.Now().Add(-time.Minute)) // already expired
	b.Add("revoked-2", time.Now().Add(time.Hour))
	if b.Contains("revoked-1") {
		t.Error("expired jti should have been cleaned up")
	}
	if !b.Contains("revoked-2") {
		t.Error("active jti should be revoked")
	}
}

func TestEmptySecretStillWorks(t *testing.T) {
	m := NewManager("", time.Hour, "")
	token, _, err := m.Issue("uid-1", "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Parse(token); err != nil {
		t.Errorf("Parse: %v", err)
	}
}
