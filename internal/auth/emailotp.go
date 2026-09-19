package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Email OTP constants. The verification code is short-lived, single-use and
// has a small attempt budget to deter brute force.
const (
	emailCodeLength     = 6
	emailCodeLifetime   = 10 * time.Minute
	emailCodeMaxTry     = 5
	emailResendInterval = 60 * time.Second
)

// Errors returned by EmailOTPManager.
var (
	ErrEmailCodeNotFound  = errors.New("email verification code not found")
	ErrEmailCodeExpired   = errors.New("email verification code expired")
	ErrEmailCodeAttempts  = errors.New("too many verification attempts")
	ErrEmailCodeMismatch  = errors.New("verification code mismatch")
	ErrEmailResendTooSoon = errors.New("verification code resent too soon")
)

type emailOTPEntry struct {
	code      string
	expiresAt time.Time
	createdAt time.Time
	attempts  int
}

// EmailOTPManager issues and verifies short-lived email verification codes,
// keyed by a normalized email address. It is safe for concurrent use.
type EmailOTPManager struct {
	mu    sync.Mutex
	codes map[string]*emailOTPEntry
}

// NewEmailOTPManager creates an empty manager.
func NewEmailOTPManager() *EmailOTPManager {
	return &EmailOTPManager{codes: make(map[string]*emailOTPEntry)}
}

// Generate creates a fresh code for the email, invalidating any previous one.
// It returns the code (for the email body) and whether a new code was actually
// issued. If a valid code was issued within emailResendInterval it is kept and
// resent=false is returned so the caller can skip sending a second email.
func (m *EmailOTPManager) Generate(email string) (code string, resent bool, err error) {
	email = normalizeEmail(email)
	m.mu.Lock()
	defer m.mu.Unlock()

	now := timeNow()
	if old, ok := m.codes[email]; ok && old.expiresAt.After(now) && now.Sub(old.createdAt) < emailResendInterval {
		return old.code, false, nil
	}

	code, err = randomDigits(emailCodeLength)
	if err != nil {
		return "", false, err
	}
	m.codes[email] = &emailOTPEntry{
		code:      code,
		createdAt: now,
		expiresAt: now.Add(emailCodeLifetime),
	}
	return code, true, nil
}

// Verify checks a code for the email, consuming it on success. Wrong codes
// count toward a per-code attempt budget; the code is invalidated once the
// budget is exhausted.
func (m *EmailOTPManager) Verify(email, code string) error {
	email = normalizeEmail(email)
	code = strings.TrimSpace(code)
	m.mu.Lock()
	defer m.mu.Unlock()

	now := timeNow()
	entry, ok := m.codes[email]
	if !ok || entry == nil {
		return ErrEmailCodeNotFound
	}
	if now.After(entry.expiresAt) {
		delete(m.codes, email)
		return ErrEmailCodeExpired
	}
	if entry.attempts >= emailCodeMaxTry {
		delete(m.codes, email)
		return ErrEmailCodeAttempts
	}

	// Constant-time comparison of same-length strings.
	if len(code) == len(entry.code) && subtle.ConstantTimeCompare([]byte(code), []byte(entry.code)) == 1 {
		delete(m.codes, email)
		return nil
	}
	entry.attempts++
	return ErrEmailCodeMismatch
}

// Invalidate removes any pending code for the email (e.g. after a successful
// action that was gated by it).
func (m *EmailOTPManager) Invalidate(email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.codes, normalizeEmail(email))
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// randomDigits returns n random decimal digits as a string.
func randomDigits(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range buf {
		sb.WriteByte(byte('0' + int(b)%10))
	}
	return sb.String(), nil
}

// ValidateEmail is a light sanity check for the registration/update forms.
func ValidateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("邮箱不能为空")
	}
	if len(email) > 190 {
		return fmt.Errorf("邮箱过长")
	}
	if strings.ContainsAny(email, " \t\r\n") {
		return fmt.Errorf("邮箱格式不正确")
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 || strings.IndexByte(email[at+1:], '@') >= 0 {
		return fmt.Errorf("邮箱格式不正确")
	}
	dot := strings.LastIndexByte(email[at:], '.')
	if dot <= 1 || dot == len(email[at:])-1 {
		return fmt.Errorf("邮箱格式不正确")
	}
	return nil
}
