package lottery

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	// Register image formats (png, jpeg, gif) for image.Decode.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// ErrQRContainsLink means the QR payload is a URL. Real 体彩/大乐透 ticket QR
// codes only carry a validation link (e.g. https://s.sporttery.cn/HD/wyWLPG),
// not the printed numbers, so they cannot be parsed as a bet.
var ErrQRContainsLink = errors.New("QR code contains a link, not ticket numbers")

// IsURL reports whether s looks like an http(s) URL.
func IsURL(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// QRDecoder decodes QR-code images into text.
type QRDecoder struct{}

// Decode reads a QR code from raw image bytes and returns its text payload.
func (d *QRDecoder) Decode(data []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}

	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", fmt.Errorf("build binary bitmap: %w", err)
	}

	reader := qrcode.NewQRCodeReader()
	result, err := reader.Decode(bmp, nil)
	if err != nil {
		return "", fmt.Errorf("no QR code found in image: %w", err)
	}
	if result.GetText() == "" {
		return "", fmt.Errorf("QR code is empty")
	}
	return result.GetText(), nil
}

// ScanTicket decodes a QR-code image and parses the 大乐透 numbers from it.
// The QR payload is expected to contain 7 numbers: 5 front + 2 back, e.g.
// "05,12,18,23,35,01,08" or "05 12 18 23 35 + 01 08". If the payload is a
// link (which is what real printed tickets carry) it returns ErrQRContainsLink.
func (d *QRDecoder) ScanTicket(data []byte) (*SuperLotto, error) {
	text, err := d.Decode(data)
	if err != nil {
		return nil, err
	}
	if IsURL(text) {
		return nil, fmt.Errorf("%w: %q; 真实票面二维码只含校验链接，请提交票面号码（见 /lottery/verify-ticket）", ErrQRContainsLink, text)
	}
	ticket, err := ParseSuperLotto(text)
	if err != nil {
		return nil, fmt.Errorf("parse numbers from QR payload %q: %w", text, err)
	}
	return ticket, nil
}
