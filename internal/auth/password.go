package auth

import (
	"fmt"
	"regexp"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work factor for password hashing.
const bcryptCost = bcrypt.DefaultCost // 10

// HashPassword returns a bcrypt hash of the plaintext password.
func HashPassword(plain string) (string, error) {
	if utf8.RuneCountInString(plain) < 6 {
		return "", fmt.Errorf("password must be at least 6 characters")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	return string(b), err
}

// CheckPassword reports whether the plaintext matches the bcrypt hash.
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

// ValidateUsername checks the username format (3-32 alphanumeric/underscore).
func ValidateUsername(username string) error {
	if !usernameRe.MatchString(username) {
		return fmt.Errorf("username must be 3-32 characters of letters, digits or underscore")
	}
	return nil
}
