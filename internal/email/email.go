package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"

	"alice/internal/config"
)

var smtpDial = net.Dial

// Sender sends an email message.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTPSender sends email via SMTP with optional STARTTLS.
type SMTPSender struct {
	host     string
	port     int
	username string
	password string
	from     string
	useTLS   bool
}

// NoopSender logs the OTP code to slog.Warn instead of sending real email.
// It is the development fallback when ALICE_SMTP_HOST=noop.
type NoopSender struct{}

// NewSenderFromConfig constructs an appropriate Sender from config.
// Returns nil when no SMTP host is configured (OTP flow disabled).
// Returns a NoopSender when host is "noop".
// Returns an SMTPSender otherwise.
func NewSenderFromConfig(cfg config.Config) Sender {
	if cfg.SMTPHost == "" {
		return nil
	}
	if cfg.SMTPHost == "noop" {
		slog.Warn("email sender configured in noop mode; OTP codes will be logged to stderr, not sent")
		return &NoopSender{}
	}
	return &SMTPSender{
		host:     cfg.SMTPHost,
		port:     cfg.SMTPPort,
		username: cfg.SMTPUsername,
		password: cfg.SMTPPassword,
		from:     cfg.SMTPFrom,
		useTLS:   cfg.SMTPTLS,
	}
}

// Send delivers an email via SMTP. The SMTP password is never logged.
func (s *SMTPSender) Send(_ context.Context, to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	msg := []byte(
		"From: " + s.from + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"\r\n" +
			body + "\r\n",
	)

	var auth smtp.Auth
	if s.username != "" {
		auth = smtp.PlainAuth("", s.username, s.password, s.host)
	}

	if s.useTLS {
		return s.sendWithSTARTTLS(addr, auth, to, msg)
	}
	return s.sendWithoutTLS(addr, auth, to, msg)
}

func (s *SMTPSender) sendWithoutTLS(addr string, auth smtp.Auth, to string, msg []byte) error {
	conn, err := smtpDial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Close() //nolint:errcheck

	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}

	if err := client.Mail(s.from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data writer: %w", err)
	}

	return client.Quit()
}

func (s *SMTPSender) sendWithSTARTTLS(addr string, auth smtp.Auth, to string, msg []byte) error {
	conn, err := smtpDial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Close() //nolint:errcheck

	tlsConfig := &tls.Config{ServerName: s.host} //nolint:gosec
	if err := client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(s.from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data writer: %w", err)
	}

	return client.Quit()
}

// Send logs the OTP code as a warning. Never sends a real email.
func (s *NoopSender) Send(_ context.Context, to, subject, body string) error {
	slog.Warn("noop email sender: would send email", "to", to, "subject", subject, "body", body)
	return nil
}
