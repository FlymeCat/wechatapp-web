package mail

import (
	"strings"
	"testing"
)

func TestNewSenderDefaultsToConsole(t *testing.T) {
	if _, ok := NewSender(Config{}).(*Console); !ok {
		t.Error("empty config should produce the console sender")
	}
	if _, ok := NewSender(Config{Mode: "console"}).(*Console); !ok {
		t.Error("mode=console should produce the console sender")
	}
	if _, ok := NewSender(Config{Mode: "SMTP"}).(*SMTP); !ok {
		t.Error("mode=smtp should produce the SMTP sender")
	}
}

func TestConsoleSend(t *testing.T) {
	c := &Console{}
	if err := c.Send("", "a@b.co", "subject", "body"); err != nil {
		t.Fatal(err)
	}
}

func TestSMTPRequiresHost(t *testing.T) {
	s := &SMTP{cfg: Config{Mode: "smtp"}}
	if err := s.Send("", "a@b.co", "s", "b"); err == nil || !strings.Contains(err.Error(), "host") {
		t.Errorf("expected host error, got %v", err)
	}
}
