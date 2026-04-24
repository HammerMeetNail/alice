package email_test

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
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

// startFakeSMTP spins up a minimal in-process SMTP server on a random localhost
// port that accepts exactly one connection. It records the DATA section and
// returns the server address plus a function that blocks until the client sends
// QUIT and then returns the received message body.
func startFakeSMTP(t *testing.T) (addr string, received func() string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck

	msgCh := make(chan string, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			msgCh <- ""
			return
		}
		defer conn.Close() //nolint:errcheck

		r := bufio.NewReader(conn)
		writeLine := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

		writeLine("220 test ESMTP")

		var body strings.Builder
		inData := false

		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			cmd := strings.TrimRight(line, "\r\n")

			if inData {
				if cmd == "." {
					inData = false
					writeLine("250 OK")
					continue
				}
				// Strip leading dot-stuffing per RFC 5321 §4.5.2.
				if strings.HasPrefix(cmd, "..") {
					cmd = cmd[1:]
				}
				body.WriteString(cmd + "\n")
				continue
			}

			upper := strings.ToUpper(cmd)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				writeLine("250-test")
				writeLine("250-AUTH PLAIN LOGIN")
				writeLine("250 OK")
			case strings.HasPrefix(upper, "AUTH"):
				// Accept any credentials — we test our code's wiring, not auth validation.
				writeLine("235 Authentication successful")
			case strings.HasPrefix(upper, "MAIL FROM"):
				writeLine("250 OK")
			case strings.HasPrefix(upper, "RCPT TO"):
				writeLine("250 OK")
			case upper == "DATA":
				writeLine("354 Start input; end with <CRLF>.<CRLF>")
				inData = true
			case upper == "QUIT":
				writeLine("221 Bye")
				msgCh <- body.String()
				return
			default:
				writeLine("500 Unknown command")
			}
		}
		msgCh <- body.String()
	}()

	return ln.Addr().String(), func() string { return <-msgCh }
}

// splitHostPort parses host and port from addr without importing strconv at
// call sites.
func splitHostPort(t *testing.T, addr string) (host string, port int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host/port %q: %v", addr, err)
	}
	portInt, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("parse port %q: %v", p, err)
	}
	return h, portInt
}

func TestSMTPSender_Send_NonTLS(t *testing.T) {
	addr, received := startFakeSMTP(t)
	host, port := splitHostPort(t, addr)

	sender := email.NewSenderFromConfig(config.Config{
		SMTPHost: host,
		SMTPPort: port,
		SMTPFrom: "from@example.com",
		SMTPTLS:  false,
	})

	err := sender.Send(context.Background(), "to@example.com", "Your OTP", "your code is 123456")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	body := received()

	if !strings.Contains(body, "Subject: Your OTP") {
		t.Errorf("message missing Subject header; body:\n%s", body)
	}
	if !strings.Contains(body, "From: from@example.com") {
		t.Errorf("message missing From header; body:\n%s", body)
	}
	if !strings.Contains(body, "To: to@example.com") {
		t.Errorf("message missing To header; body:\n%s", body)
	}
	if !strings.Contains(body, "123456") {
		t.Errorf("message body missing OTP code; body:\n%s", body)
	}
}

func TestSMTPSender_Send_NonTLS_WithAuth(t *testing.T) {
	addr, received := startFakeSMTP(t)
	host, port := splitHostPort(t, addr)

	// The fake SMTP server does not advertise AUTH, so PlainAuth will be skipped
	// by smtp.SendMail. Verify the message still arrives correctly.
	sender := email.NewSenderFromConfig(config.Config{
		SMTPHost:     host,
		SMTPPort:     port,
		SMTPFrom:     "sender@example.com",
		SMTPUsername: "user",
		SMTPPassword: "pass",
		SMTPTLS:      false,
	})

	err := sender.Send(context.Background(), "recipient@example.com", "Hello", "test body")
	if err != nil {
		t.Fatalf("Send with auth config: %v", err)
	}
	body := received()
	if !strings.Contains(body, "test body") {
		t.Errorf("message body not present; got:\n%s", body)
	}
}

func TestSMTPSender_Send_TLS_DialError(t *testing.T) {
	// Port 1 is almost certainly not listening; net.Dial should fail quickly.
	sender := email.NewSenderFromConfig(config.Config{
		SMTPHost: "127.0.0.1",
		SMTPPort: 1,
		SMTPFrom: "from@example.com",
		SMTPTLS:  true,
	})

	err := sender.Send(context.Background(), "to@example.com", "Subject", "body")
	if err == nil {
		t.Fatal("expected dial error for port 1, got nil")
	}
	if !strings.Contains(err.Error(), "dial smtp") {
		t.Errorf("expected 'dial smtp' in error, got: %v", err)
	}
}

func TestSMTPSender_Send_TLS_ClientError(t *testing.T) {
	// Spin up a listener that immediately closes the connection after accepting.
	// smtp.NewClient should fail because it never receives the SMTP greeting.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close() // drop the connection immediately
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	sender := email.NewSenderFromConfig(config.Config{
		SMTPHost: host,
		SMTPPort: port,
		SMTPFrom: "from@example.com",
		SMTPTLS:  true,
	})

	err = sender.Send(context.Background(), "to@example.com", "Subject", "body")
	if err == nil {
		t.Fatal("expected error when server closes connection immediately, got nil")
	}
}
