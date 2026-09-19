// Package mail sends emails (currently used for MFA reset verification codes).
package mail

import (
	"fmt"
	"log"
	"net/smtp"
	"strings"
)

// Mode constants for NewSender.
const (
	ModeConsole = "console" // print the email to the server log (development)
	ModeSMTP    = "smtp"    // deliver via an SMTP server
)

// Sender delivers an email.
type Sender interface {
	// Send delivers a plain-text email. from may be empty when the sender
	// uses a configured default.
	Send(from, to, subject, body string) error
}

// Config configures a Sender.
type Config struct {
	Mode     string // console | smtp (empty means console)
	Host     string // smtp host, e.g. smtp.qq.com
	Port     string // smtp port, e.g. 465 / 587
	User     string // smtp username (usually the from address)
	Password string // smtp password or app-specific token
	From     string // display/from address used when User is empty
}

// NewSender builds a Sender from cfg. An empty Mode defaults to console so the
// service runs out of the box without an SMTP server.
func NewSender(cfg Config) Sender {
	switch strings.ToLower(cfg.Mode) {
	case ModeSMTP:
		return &SMTP{cfg: cfg}
	default:
		return &Console{}
	}
}

// Console prints the email to the standard logger. It is the default sender so
// the MFA reset flow is usable in development without configuring SMTP.
type Console struct{}

// Send implements Sender by logging the message.
func (c *Console) Send(from, to, subject, body string) error {
	log.Printf("[mail:console] to=%s subject=%q\n%s", to, subject, body)
	return nil
}

// SMTP delivers email through a standard SMTP server using PLAIN auth over
// STARTTLS (net/smtp upgrades to TLS when the server advertises it).
type SMTP struct {
	cfg Config
}

// Send implements Sender via net/smtp.
func (s *SMTP) Send(from, to, subject, body string) error {
	cfg := s.cfg
	if cfg.Host == "" {
		return fmt.Errorf("smtp host is not configured")
	}
	from = firstNonEmpty(from, cfg.From, cfg.User)
	addr := cfg.Host
	if cfg.Port != "" {
		addr += ":" + cfg.Port
	}
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")
	var auth smtp.Auth
	if cfg.User != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, []byte(msg))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
