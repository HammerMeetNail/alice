package email

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"alice/internal/config"
)

func TestNoopSender_Send(t *testing.T) {
	sender := &NoopSender{}
	err := sender.Send(context.Background(), "to@example.com", "subject", "body")
	if err != nil {
		t.Fatalf("NoopSender.Send returned unexpected error: %v", err)
	}
}

func TestNewSenderFromConfig_NoHost_ReturnsNil(t *testing.T) {
	cfg := config.Config{SMTPHost: ""}
	s := NewSenderFromConfig(cfg)
	if s != nil {
		t.Fatalf("expected nil sender for empty SMTP host, got %T", s)
	}
}

func TestNewSenderFromConfig_NoopHost_ReturnsNoopSender(t *testing.T) {
	cfg := config.Config{SMTPHost: "noop"}
	s := NewSenderFromConfig(cfg)
	if s == nil {
		t.Fatal("expected non-nil sender for noop host")
	}
	if _, ok := s.(*NoopSender); !ok {
		t.Fatalf("expected *NoopSender, got %T", s)
	}
}

func TestNewSenderFromConfig_RealHost_ReturnsSMTPSender(t *testing.T) {
	cfg := config.Config{
		SMTPHost: "smtp.example.com",
		SMTPPort: 587,
		SMTPFrom: "noreply@example.com",
	}
	s := NewSenderFromConfig(cfg)
	if s == nil {
		t.Fatal("expected non-nil sender for real SMTP host")
	}
	// SMTPSender is unexported fields only; verify it's not NoopSender.
	if _, ok := s.(*NoopSender); ok {
		t.Fatal("expected SMTPSender, got NoopSender")
	}
}

// startFakeSMTP serves one SMTP conversation over net.Pipe. It records the
// DATA section and returns a dial hook plus a function that waits for the
// client to finish and returns the received message body.
func startFakeSMTP(t *testing.T) (dial func(string, string) (net.Conn, error), received func() string) {
	t.Helper()

	serverConn, clientConn := net.Pipe()

	msgCh := make(chan string, 1)

	go func() {
		defer serverConn.Close() //nolint:errcheck

		r := bufio.NewReader(serverConn)
		writeLine := func(s string) { _, _ = serverConn.Write([]byte(s + "\r\n")) }

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

	return func(string, string) (net.Conn, error) {
		return clientConn, nil
	}, func() string { return <-msgCh }
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
	dial, received := startFakeSMTP(t)
	oldDial := smtpDial
	smtpDial = dial
	t.Cleanup(func() { smtpDial = oldDial })

	sender := NewSenderFromConfig(config.Config{
		SMTPHost: "smtp.test",
		SMTPPort: 25,
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
	dial, received := startFakeSMTP(t)
	oldDial := smtpDial
	smtpDial = dial
	t.Cleanup(func() { smtpDial = oldDial })

	// PlainAuth requires TLS or localhost. Use a localhost host string so the
	// standard-library auth path still runs under the pipe-backed test server.
	sender := NewSenderFromConfig(config.Config{
		SMTPHost:     "127.0.0.1",
		SMTPPort:     25,
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
	oldDial := smtpDial
	smtpDial = func(string, string) (net.Conn, error) {
		return nil, errors.New("forced dial failure")
	}
	t.Cleanup(func() { smtpDial = oldDial })

	sender := NewSenderFromConfig(config.Config{
		SMTPHost: "smtp.test",
		SMTPPort: 25,
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
	serverConn, clientConn := net.Pipe()
	oldDial := smtpDial
	smtpDial = func(string, string) (net.Conn, error) {
		return clientConn, nil
	}
	t.Cleanup(func() { smtpDial = oldDial })

	go func() {
		serverConn.Close() // drop the connection immediately
	}()

	sender := NewSenderFromConfig(config.Config{
		SMTPHost: "smtp.test",
		SMTPPort: 25,
		SMTPFrom: "from@example.com",
		SMTPTLS:  true,
	})

	err := sender.Send(context.Background(), "to@example.com", "Subject", "body")
	if err == nil {
		t.Fatal("expected error when server closes connection immediately, got nil")
	}
}

// startSTARTTLSRejectServer starts a fake SMTP server that advertises no STARTTLS
// capability and responds with a 502 error to any STARTTLS command, exercising
// the sendWithSTARTTLS error-return path.
func startSTARTTLSRejectServer(t *testing.T) func(string, string) (net.Conn, error) {
	t.Helper()

	serverConn, clientConn := net.Pipe()

	go func() {
		defer serverConn.Close() //nolint:errcheck

		r := bufio.NewReader(serverConn)
		writeLine := func(s string) { _, _ = serverConn.Write([]byte(s + "\r\n")) }

		writeLine("220 reject-starttls ESMTP")

		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimRight(line, "\r\n"))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				// Deliberately omit STARTTLS from capabilities.
				writeLine("250-reject-starttls")
				writeLine("250 OK")
			case strings.HasPrefix(cmd, "STARTTLS"):
				writeLine("502 5.5.1 Command not implemented")
				return
			default:
				writeLine("500 Unknown command")
			}
		}
	}()

	return func(string, string) (net.Conn, error) {
		return clientConn, nil
	}
}

func TestSMTPSender_Send_TLS_STARTTLSRejected(t *testing.T) {
	oldDial := smtpDial
	smtpDial = startSTARTTLSRejectServer(t)
	t.Cleanup(func() { smtpDial = oldDial })

	sender := NewSenderFromConfig(config.Config{
		SMTPHost: "smtp.test",
		SMTPPort: 25,
		SMTPFrom: "from@example.com",
		SMTPTLS:  true,
	})

	err := sender.Send(context.Background(), "to@example.com", "Subject", "body")
	if err == nil {
		t.Fatal("expected error when STARTTLS is rejected, got nil")
	}
	if !strings.Contains(err.Error(), "smtp starttls") {
		t.Errorf("expected 'smtp starttls' in error, got: %v", err)
	}
}
