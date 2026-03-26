package email_test

import (
	"context"
	"testing"

	"alice/internal/config"
	"alice/internal/email"
)

func TestNoopSender_Send(t *testing.T) {
	sender := &email.NoopSender{}
	err := sender.Send(context.Background(), "to@example.com", "subject", "body")
	if err != nil {
		t.Fatalf("NoopSender.Send returned unexpected error: %v", err)
	}
}

func TestNewSenderFromConfig_NoHost_ReturnsNil(t *testing.T) {
	cfg := config.Config{SMTPHost: ""}
	s := email.NewSenderFromConfig(cfg)
	if s != nil {
		t.Fatalf("expected nil sender for empty SMTP host, got %T", s)
	}
}

func TestNewSenderFromConfig_NoopHost_ReturnsNoopSender(t *testing.T) {
	cfg := config.Config{SMTPHost: "noop"}
	s := email.NewSenderFromConfig(cfg)
	if s == nil {
		t.Fatal("expected non-nil sender for noop host")
	}
	if _, ok := s.(*email.NoopSender); !ok {
		t.Fatalf("expected *email.NoopSender, got %T", s)
	}
}

func TestNewSenderFromConfig_RealHost_ReturnsSMTPSender(t *testing.T) {
	cfg := config.Config{
		SMTPHost: "smtp.example.com",
		SMTPPort: 587,
		SMTPFrom: "noreply@example.com",
	}
	s := email.NewSenderFromConfig(cfg)
	if s == nil {
		t.Fatal("expected non-nil sender for real SMTP host")
	}
	// SMTPSender is unexported fields only; verify it's not NoopSender.
	if _, ok := s.(*email.NoopSender); ok {
		t.Fatal("expected SMTPSender, got NoopSender")
	}
}
