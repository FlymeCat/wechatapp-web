package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// contextKey is a private type for gin.Context keys.
type contextKey string

const claimsKey contextKey = "auth.claims"

// ClaimsFromContext returns the authenticated claims stored by RequireAuth.
func ClaimsFromContext(c *gin.Context) (*Claims, bool) {
	v, ok := c.Get(string(claimsKey))
	if !ok {
		return nil, false
	}
	cl, ok := v.(*Claims)
	return cl, ok
}

// UserIDFromContext returns the authenticated user ID, or "" if absent.
func UserIDFromContext(c *gin.Context) string {
	if cl, ok := ClaimsFromContext(c); ok {
		return cl.UserID
	}
	return ""
}

// RequireAuth validates the "Authorization: Bearer <token>" header and stores
// the claims in the context. Requests without a valid token get 401.
func (m *Manager) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		tokenStr, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || strings.TrimSpace(tokenStr) == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed Authorization header"})
			return
		}

		claims, err := m.Parse(strings.TrimSpace(tokenStr))
		if err != nil {
			msg := "invalid or expired token"
			switch {
			case errors.Is(err, ErrTokenExpired):
				msg = "token expired, please login again"
			case errors.Is(err, ErrTokenRevoked):
				msg = "token revoked, please login again"
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": msg})
			return
		}
		if claims.Purpose == PurposeMFAChallenge {
			// A step-2 challenge token is only valid at /auth/mfa/verify.
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "MFA verification required"})
			return
		}

		c.Set(string(claimsKey), claims)
		c.Next()
	}
}

// RequireMFAChallenge validates a step-2 challenge token and stores its claims
// (used by /auth/mfa/verify). Access tokens are rejected here.
func (m *Manager) RequireMFAChallenge() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		tokenStr, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || strings.TrimSpace(tokenStr) == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed Authorization header"})
			return
		}
		claims, err := m.Parse(strings.TrimSpace(tokenStr))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "MFA challenge invalid or expired, please login again"})
			return
		}
		if claims.Purpose != PurposeMFAChallenge {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "expected an MFA challenge token"})
			return
		}
		c.Set(string(claimsKey), claims)
		c.Next()
	}
}

// RequireRole restricts a route to authenticated users holding one of the
// given roles. Use after RequireAuth.
func RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c *gin.Context) {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if !allowed[claims.Role] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permission"})
			return
		}
		c.Next()
	}
}
