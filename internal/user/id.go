package user

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NewID returns a random 16-byte hex identifier.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is practically impossible; fall back to a
		// timestamp-based ID so we never block on it.
		return timeID()
	}
	return hex.EncodeToString(b)
}

func timeID() string {
	return fmt.Sprintf("t%x", time.Now().UnixNano())
}
