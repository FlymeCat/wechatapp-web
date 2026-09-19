package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// TestVerifyTOTPKnownVector checks a code generated for a fixed secret and
// instant, plus acceptance inside the drift window and rejection outside it.
func TestVerifyTOTPKnownVector(t *testing.T) {
	key, err := NewTOTPKey(TOTPParams{Issuer: "wechatapp-web", AccountName: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	secret := key.Secret()

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	// Restore the clock after the test.
	orig := timeNow
	defer func() { timeNow = orig }()

	timeNow = func() time.Time { return now }
	if !VerifyTOTP(secret, code) {
		t.Errorf("code %s should verify at its own instant", code)
	}

	// One step of drift (30s either way) is accepted.
	timeNow = func() time.Time { return now.Add(30 * time.Second) }
	if !VerifyTOTP(secret, code) {
		t.Error("code should verify within +1 step")
	}
	timeNow = func() time.Time { return now.Add(-30 * time.Second) }
	if !VerifyTOTP(secret, code) {
		t.Error("code should verify within -1 step")
	}

	// Far outside the window it must fail.
	timeNow = func() time.Time { return now.Add(5 * time.Minute) }
	if VerifyTOTP(secret, code) {
		t.Error("code should not verify far outside the drift window")
	}
}

func TestVerifyTOTPRejectsBadInput(t *testing.T) {
	key, _ := NewTOTPKey(TOTPParams{AccountName: "alice"})
	if VerifyTOTP("", "123456") {
		t.Error("empty secret must not verify")
	}
	if VerifyTOTP(key.Secret(), "") {
		t.Error("empty code must not verify")
	}
	if VerifyTOTP(key.Secret(), "abcdef") {
		t.Error("non-numeric code must not verify")
	}
	if VerifyTOTP(key.Secret(), "000000") {
		t.Error("random code must not verify")
	}
}

func TestNewTOTPKeyProvisioningURI(t *testing.T) {
	key, err := NewTOTPKey(TOTPParams{Issuer: "wechatapp-web", AccountName: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	url := key.URL()
	if !strings.HasPrefix(url, "otpauth://totp/") {
		t.Errorf("url = %q, want otpauth://totp/ prefix", url)
	}
	for _, want := range []string{"secret=" + key.Secret(), "issuer=wechatapp-web", "alice"} {
		if !strings.Contains(url, want) {
			t.Errorf("url %q missing %q", url, want)
		}
	}
	if len(key.Secret()) < 16 {
		t.Errorf("secret too short: %q", key.Secret())
	}
}

func TestQRCodePNG(t *testing.T) {
	key, _ := NewTOTPKey(TOTPParams{AccountName: "alice"})
	png, err := QRCodePNG(key)
	if err != nil {
		t.Fatal(err)
	}
	// PNG magic bytes.
	if len(png) < 8 || string(png[1:4]) != "PNG" {
		t.Errorf("not a PNG: % x", png[:min(8, len(png))])
	}
}

func TestEmailOTPGenerateVerify(t *testing.T) {
	m := NewEmailOTPManager()
	code, fresh, err := m.Generate("Alice@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Error("first generation should be fresh")
	}
	if len(code) != emailCodeLength {
		t.Errorf("code = %q, want %d digits", code, emailCodeLength)
	}

	// Email is normalized: case and whitespace-insensitive.
	if err := m.Verify("  alice@example.com ", code); err != nil {
		t.Errorf("verify with normalized email failed: %v", err)
	}
	// Single use: the same code is gone.
	if err := m.Verify("alice@example.com", code); err != ErrEmailCodeNotFound {
		t.Errorf("second verify = %v, want ErrEmailCodeNotFound", err)
	}
}

func TestEmailOTPWrongCodeAndAttemptBudget(t *testing.T) {
	m := NewEmailOTPManager()
	code, _, err := m.Generate("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	// A wrong code counts as an attempt.
	for i := 0; i < emailCodeMaxTry; i++ {
		if err := m.Verify("alice@example.com", "000000"); err != ErrEmailCodeMismatch {
			t.Fatalf("attempt %d: err = %v, want ErrEmailCodeMismatch", i+1, err)
		}
	}
	// Budget exhausted: the code is invalidated even if we now send the right one.
	if err := m.Verify("alice@example.com", code); err != ErrEmailCodeAttempts {
		t.Errorf("verify after budget = %v, want ErrEmailCodeAttempts", err)
	}
	// A fresh code restores the budget.
	if _, _, err := m.Generate("alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.Verify("alice@example.com", "000000"); err != ErrEmailCodeMismatch {
		t.Errorf("err = %v, want ErrEmailCodeMismatch", err)
	}
}

func TestEmailOTPExpiry(t *testing.T) {
	orig := timeNow
	defer func() { timeNow = orig }()

	m := NewEmailOTPManager()
	code, _, err := m.Generate("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	base := orig()
	timeNow = func() time.Time { return base.Add(emailCodeLifetime + time.Second) }
	if err := m.Verify("alice@example.com", code); err != ErrEmailCodeExpired {
		t.Errorf("verify after expiry = %v, want ErrEmailCodeExpired", err)
	}
}

func TestEmailOTPResendCooldown(t *testing.T) {
	orig := timeNow
	defer func() { timeNow = orig }()

	m := NewEmailOTPManager()
	code1, fresh1, err := m.Generate("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !fresh1 {
		t.Fatal("first generate should be fresh")
	}
	// Within the resend window the same code is kept and marked resent.
	code2, fresh2, err := m.Generate("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if fresh2 {
		t.Error("generate inside cooldown should not be fresh")
	}
	if code2 != code1 {
		t.Errorf("code changed inside cooldown: %q -> %q", code1, code2)
	}
	// After the cooldown a fresh code is issued.
	base := orig()
	timeNow = func() time.Time { return base.Add(emailResendInterval + time.Second) }
	code3, fresh3, err := m.Generate("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !fresh3 {
		t.Error("generate after cooldown should be fresh")
	}
	if code3 == code1 {
		t.Error("code should have changed after cooldown")
	}
}

func TestEmailOTPInvalidate(t *testing.T) {
	m := NewEmailOTPManager()
	code, _, err := m.Generate("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	m.Invalidate("ALICE@example.com")
	if err := m.Verify("alice@example.com", code); err != ErrEmailCodeNotFound {
		t.Errorf("verify after invalidate = %v, want ErrEmailCodeNotFound", err)
	}
}

func TestValidateEmail(t *testing.T) {
	for _, ok := range []string{"a@b.co", "user.name+tag@example.com", "  padded@example.com  "} {
		if err := ValidateEmail(ok); err != nil {
			t.Errorf("ValidateEmail(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "no-at-sign", "a@", "@b.co", "a@b", "a b@c.co", "a@b@c.co"} {
		if err := ValidateEmail(bad); err == nil {
			t.Errorf("ValidateEmail(%q) = nil, want error", bad)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
