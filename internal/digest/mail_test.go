package digest

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// stallingSMTP accepts connections and then says nothing at all — the failure
// mode that used to pin the digest scheduler forever, because smtp.SendMail
// neither takes a context nor sets a socket deadline.
func stallingSMTP(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open and never send the 220 greeting.
			go func() {
				<-done
				_ = conn.Close()
			}()
		}
	}()
	t.Cleanup(func() {
		close(done)
		_ = ln.Close()
		wg.Wait()
	})

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func mailCfg(host string, port int) MailConfig {
	return MailConfig{Enabled: true, Host: host, Port: port, From: "bb@example.com", To: "ops@example.com"}
}

// A stalled server must not outlive the caller's deadline.
func TestDeliverEmailHonorsContextDeadline(t *testing.T) {
	t.Parallel()
	host, port := stallingSMTP(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := DeliverEmail(ctx, mailCfg(host, port), "ops@example.com", "subj", "plain", "<p>html</p>")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error from a stalled SMTP server, got nil")
	}
	// Generous bound: the point is that it returns at all, near the deadline,
	// rather than blocking on a server that never speaks.
	if elapsed > 20*time.Second {
		t.Errorf("took %v — the deadline was not honored", elapsed)
	}
}

// Even with no deadline on the caller's context, every attempt is bounded, so
// no configuration can leave the digest scheduler goroutine stuck for good.
// Not parallel: it shrinks the package-level attempt timeout.
func TestDeliverEmailBoundedWithoutDeadline(t *testing.T) {
	host, port := stallingSMTP(t)

	restore := smtpAttemptTimeout
	smtpAttemptTimeout = 150 * time.Millisecond
	t.Cleanup(func() { smtpAttemptTimeout = restore })

	done := make(chan error, 1)
	go func() {
		done <- DeliverEmail(context.Background(), mailCfg(host, port), "ops@example.com", "s", "p", "h")
	}()

	// Worst case is 3 shrunk attempts plus the 1s+3s backoff between them.
	// Before the fix this never returned at all: smtp.SendMail neither took a
	// context nor set a socket deadline, so a server that accepts and then
	// says nothing pinned the caller forever.
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error from a stalled server, got nil")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("DeliverEmail never returned — an attempt is unbounded")
	}
}

// An already-canceled context must not even attempt a send.
func TestDeliverEmailCancelledContextDoesNotDial(t *testing.T) {
	t.Parallel()

	// Port 1 on a black-hole address: any real dial attempt would be obvious.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := DeliverEmail(ctx, mailCfg("192.0.2.1", 1), "ops@example.com", "s", "p", "h")
	if err == nil {
		t.Fatal("want an error for a canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("canceled context still took %v", elapsed)
	}
}

// The retry backoff must be interruptible: a context canceled mid-backoff
// should cut the loop short rather than sleeping out the remaining delays.
func TestDeliverEmailRetryBackoffIsInterruptible(t *testing.T) {
	t.Parallel()

	// Nothing listening -> connection refused, which transientSMTPError treats
	// as retryable, so the backoff path is exercised.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close() // free the port so dials are refused

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = DeliverEmail(ctx, mailCfg(addr.IP.String(), addr.Port), "ops@example.com", "s", "p", "h")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error, got nil")
	}
	// Total backoff is 1s+3s; without the ctx-aware select this would run ~4s.
	if elapsed > 3*time.Second {
		t.Errorf("took %v — backoff did not observe the context", elapsed)
	}
}

func TestTransientSMTPError(t *testing.T) {
	t.Parallel()
	if transientSMTPError(nil) {
		t.Error("nil is not transient")
	}
	if !transientSMTPError(errors.New("dial tcp: i/o timeout")) {
		t.Error("i/o timeout should be transient")
	}
	if transientSMTPError(errors.New("535 authentication failed")) {
		t.Error("auth failure must not be retried")
	}
}

// The message we hand to the server must carry both MIME parts and an encoded
// subject; this guards the body build that sendMail now writes directly.
func TestDeliverEmailBuildsMultipart(t *testing.T) {
	t.Parallel()

	captured := make(chan capturedMail, 1)
	host, port := recordingSMTP(t, captured)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := DeliverEmail(ctx, mailCfg(host, port), "ops@example.com",
		"weekly digest", "PLAINBODY", "<p>HTMLBODY</p>"); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := awaitMail(t, captured)
	for _, want := range []string{
		"From: bb@example.com",
		"To: ops@example.com",
		"Subject: weekly digest",
		"multipart/alternative",
		"PLAINBODY",
		"<p>HTMLBODY</p>",
	} {
		if !strings.Contains(got.Data, want) {
			t.Errorf("message missing %q:\n%s", want, got.Data)
		}
	}
	// No name and no reply-to configured: the headers stay exactly as they
	// were before those fields existed.
	if strings.Contains(got.Data, "Reply-To:") {
		t.Errorf("unconfigured reply-to should emit no header:\n%s", got.Data)
	}
}

// A configured display name and reply-to must reach the recipient, while the
// SMTP envelope keeps the bare sending address — a relay that sees a display
// name in MAIL FROM rejects the message outright.
func TestDeliverEmailSetsFromNameAndReplyTo(t *testing.T) {
	t.Parallel()

	captured := make(chan capturedMail, 1)
	host, port := recordingSMTP(t, captured)

	cfg := mailCfg(host, port)
	cfg.FromName = "BugBarn (staging)"
	cfg.ReplyTo = "humans@example.com"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := DeliverEmail(ctx, cfg, "ops@example.com", "subj", "plain", "<p>html</p>"); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := awaitMail(t, captured)
	if !strings.Contains(got.Data, `From: "BugBarn (staging)" <bb@example.com>`) {
		t.Errorf("From header missing the display name:\n%s", got.Data)
	}
	if !strings.Contains(got.Data, "Reply-To: humans@example.com") {
		t.Errorf("Reply-To header missing:\n%s", got.Data)
	}
	if !strings.Contains(got.EnvelopeFrom, "<bb@example.com>") ||
		strings.Contains(got.EnvelopeFrom, "BugBarn") {
		t.Errorf("envelope sender must stay the bare address, got %q", got.EnvelopeFrom)
	}
}

// A non-ASCII display name has to be RFC 2047-encoded rather than written raw
// into the header.
func TestFormatAddress(t *testing.T) {
	t.Parallel()

	if got := formatAddress("", "bb@example.com"); got != "bb@example.com" {
		t.Errorf("no name should stay bare, got %q", got)
	}
	if got := formatAddress("BugBarn", "bb@example.com"); got != `"BugBarn" <bb@example.com>` {
		t.Errorf("got %q", got)
	}
	got := formatAddress("BugBarn ✱", "bb@example.com")
	if strings.Contains(got, "✱") {
		t.Errorf("non-ASCII name must be encoded, got %q", got)
	}
	if !strings.Contains(got, "<bb@example.com>") {
		t.Errorf("encoded name lost the address, got %q", got)
	}
}

func awaitMail(t *testing.T, captured <-chan capturedMail) capturedMail {
	t.Helper()
	select {
	case got := <-captured:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the message")
		return capturedMail{}
	}
}

// capturedMail is what the recording server saw. Envelope and headers are kept
// apart on purpose: the display name belongs in the From header only.
type capturedMail struct {
	EnvelopeFrom string
	Data         string
}

// recordingSMTP is a minimal SMTP server that accepts one message and hands
// its envelope sender and DATA payload back on the channel.
func recordingSMTP(t *testing.T, out chan<- capturedMail) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

		write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		write("220 test ESMTP")

		buf := make([]byte, 4096)
		var body strings.Builder
		inData := false
		envelopeFrom := ""
		var pending strings.Builder
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			pending.WriteString(string(buf[:n]))
			for {
				chunk := pending.String()
				idx := strings.Index(chunk, "\r\n")
				if idx < 0 {
					break
				}
				line := chunk[:idx]
				pending.Reset()
				pending.WriteString(chunk[idx+2:])

				if inData {
					if line == "." {
						inData = false
						out <- capturedMail{EnvelopeFrom: envelopeFrom, Data: body.String()}
						write("250 ok")
						continue
					}
					body.WriteString(line + "\n")
					continue
				}
				upper := strings.ToUpper(line)
				switch {
				case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
					write("250-test")
					write("250 SIZE 10240000")
				case strings.HasPrefix(upper, "MAIL FROM"):
					envelopeFrom = strings.TrimSpace(strings.TrimPrefix(line[len("MAIL FROM"):], ":"))
					write("250 ok")
				case strings.HasPrefix(upper, "RCPT TO"):
					write("250 ok")
				case upper == "DATA":
					inData = true
					write("354 send it")
				case upper == "QUIT":
					write("221 bye")
					return
				default:
					write("250 ok")
				}
			}
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}
