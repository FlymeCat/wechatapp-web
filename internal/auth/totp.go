package auth

import (
	"bytes"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// timeNow is injectable for tests.
var timeNow = time.Now

// TOTPParams describes the authenticator-app parameters.
type TOTPParams struct {
	Issuer      string // shown as the app entry's issuer, e.g. "wechatapp-web"
	AccountName string // shown as the entry's account, e.g. the username
}

// NewTOTPKey creates a new TOTP secret and its otpauth:// provisioning URI.
func NewTOTPKey(p TOTPParams) (*otp.Key, error) {
	issuer := p.Issuer
	if issuer == "" {
		issuer = "wechatapp-web"
	}
	return totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: p.AccountName,
		Period:      30,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
}

// VerifyTOTP checks a 6-digit code against a base32 secret, allowing one step
// of clock drift in either direction.
func VerifyTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if secret == "" || code == "" {
		return false
	}
	ok, err := totp.ValidateCustom(code, secret, timeNow(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// QRCodePNG renders the provisioning URI as a PNG QR code.
func QRCodePNG(key *otp.Key) ([]byte, error) {
	img, err := key.Image(256, 256)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
