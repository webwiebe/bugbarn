package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// emailDeliveryCounter counts digest email-send attempts, built once from the
// global meter (see tracing.Meter) rather than recreated per call.
var emailDeliveryCounter metric.Int64Counter

func init() {
	emailDeliveryCounter, _ = tracing.Meter().Int64Counter(
		"bugbarn.digest.email_delivery",
		metric.WithDescription("Digest email delivery attempts, by outcome and attempt number."),
		metric.WithUnit("{attempt}"),
	)
}

// transientSMTPError returns true for network-level errors that are safe to
// retry. Auth failures and permanent rejections return false so we fail fast.
func transientSMTPError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"ETIMEDOUT", "ECONNREFUSED", "ENOTFOUND", "connection reset", "broken pipe", "socket", "i/o timeout",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// Send sends the message with up to 3 attempts, retrying only on
// transient failures (mirrors rapid-root email.ts retryEmailSend).
func Send(ctx context.Context, mc Config, to, subject, plain, html string) error {
	mc.To = to
	return deliver(ctx, mc, subject, plain, html)
}

// buildMessage renders the RFC 5322 message. from is the bare sending address,
// already resolved from From/User; it appears here wrapped in the display name
// while the caller hands the bare form to the SMTP envelope.
func buildMessage(mc Config, from, subject, plain, html string) []byte {
	boundary := "==BugBarnDigest=="
	var msg strings.Builder
	msg.WriteString("From: " + formatAddress(mc.FromName, from) + "\r\n")
	msg.WriteString("To: " + mc.To + "\r\n")
	if mc.ReplyTo != "" {
		// Bare address: the display name describes the sending instance, and
		// repeating it here would mislabel a reply mailbox that is somebody
		// else's.
		msg.WriteString("Reply-To: " + mc.ReplyTo + "\r\n")
	}
	msg.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + `"` + "\r\n\r\n")
	msg.WriteString("--" + boundary + "\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	msg.WriteString(plain + "\r\n")
	msg.WriteString("--" + boundary + "\r\n")
	msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	msg.WriteString(html + "\r\n")
	msg.WriteString("--" + boundary + "--\r\n")
	return []byte(msg.String())
}

func deliver(ctx context.Context, mc Config, subject, plain, html string) error {
	err := attemptDelivery(ctx, mc, subject, plain, html)
	if err == nil {
		return nil
	}
	class, code := classifyError(err)
	me := &Error{Class: class, Code: code}
	// Keep context cancellation visible to errors.Is; the sentinels carry no text.
	switch {
	case errors.Is(err, context.Canceled):
		me.cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		me.cause = context.DeadlineExceeded
	}
	return me
}

func attemptDelivery(ctx context.Context, mc Config, subject, plain, html string) error {
	from := mc.From
	if from == "" {
		from = mc.User
	}

	raw := buildMessage(mc, from, subject, plain, html)

	addr := fmt.Sprintf("%s:%d", mc.Host, mc.Port)
	// Only authenticate when credentials are actually configured. Building an
	// auth unconditionally meant a relay that does not advertise AUTH was
	// rejected outright ("server doesn't support AUTH") even though we had no
	// credentials to offer it — unauthenticated internal relays could never
	// work. Nothing that authenticates today changes: a configured User still
	// produces exactly the same PlainAuth.
	var auth smtp.Auth
	if mc.User != "" {
		auth = smtp.PlainAuth("", mc.User, mc.Pass, mc.Host)
	}

	delays := []time.Duration{time.Second, 3 * time.Second, 5 * time.Second}
	var lastErr error
	for attempt, delay := range delays {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}
		lastErr = sendMailTraced(ctx, mc.Host, attempt+1, func() error {
			// Bound each attempt even when the caller gave us no deadline, so
			// no configuration can leave this blocking forever.
			attemptCtx, cancel := context.WithTimeout(ctx, smtpAttemptTimeout)
			defer cancel()
			return sendMail(attemptCtx, addr, mc.Host, auth, from, []string{mc.To}, raw)
		})
		if lastErr == nil {
			return nil
		}
		if !transientSMTPError(lastErr) {
			return lastErr
		}
		if attempt < len(delays)-1 {
			slog.Warn("digest mailer: transient error, retrying",
				"attempt", attempt+1, "max_attempts", 3, "retry_in", delay, "error_class", errorClass(lastErr))
			select {
			case <-ctx.Done():
				return lastErr
			case <-time.After(delay):
			}
		}
	}
	return lastErr
}

// smtpAttemptTimeout bounds one SMTP attempt when the caller's context carries
// no deadline of its own. A var rather than a const so tests can shrink it
// instead of waiting out the real thing.
var smtpAttemptTimeout = 30 * time.Second

// sendMail is a context-aware replacement for smtp.SendMail. The stdlib helper
// dials with no timeout and never consults a context, so a server that accepts
// the TCP connection and then stalls pins the caller forever — for the weekly
// digest that means the scheduler goroutine never returns and every LATER
// digest silently stops firing too.
//
// The protocol flow mirrors smtp.SendMail exactly, including its security
// properties: STARTTLS whenever the server advertises it, and refusing to
// authenticate over an unencrypted link (smtp.PlainAuth.Start enforces that
// itself, so it still applies here).
func sendMail(ctx context.Context, addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	client, err := dialSMTP(ctx, addr, host, auth)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return writeMessage(client, from, to, msg)
}

// dialSMTP opens the connection and completes the greeting, STARTTLS and AUTH
// handshake, all bounded by ctx.
func dialSMTP(ctx context.Context, addr, host string, auth smtp.Auth) (*smtp.Client, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// net/smtp has no context plumbing, so translate the deadline onto the
	// socket: that is what actually bounds a mid-conversation stall.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			_ = client.Close()
			return nil, errors.New("smtp: server doesn't support AUTH")
		}
		if err := client.Auth(auth); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	return client, nil
}

// writeMessage plays the envelope and body over an established client.
func writeMessage(client *smtp.Client, from string, to []string, msg []byte) error {
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// classifyError maps a send error to a low-cardinality class and, for SMTP
// replies, the numeric code. It never returns the error text.
func classifyError(err error) (class string, code int) {
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		switch {
		case tpErr.Code == 530 || tpErr.Code == 534 || tpErr.Code == 535:
			return "auth", tpErr.Code
		case tpErr.Code >= 400 && tpErr.Code < 500:
			return "temporary_rejection", tpErr.Code
		default:
			return "permanent_rejection", tpErr.Code
		}
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "timeout", 0
	}
	var tlsErr *tls.CertificateVerificationError
	if errors.As(err, &tlsErr) {
		return "tls", 0
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "connect", 0
	}
	return "other", 0
}

func errorClass(err error) string {
	class, _ := classifyError(err)
	return class
}

// sendMailTraced wraps a single SMTP send attempt in a span, recording the
// mail host and attempt number and marking the span as failed on error. The
// existing slog-based retry logging in deliver is unchanged; this adds
// span-level visibility alongside it.
func sendMailTraced(ctx context.Context, host string, attempt int, send func() error) error {
	_, span := tracing.Tracer().Start(ctx, "digest.EmailSend")
	defer span.End()
	span.SetAttributes(
		attribute.String("smtp.host", host),
		attribute.Int("attempt", attempt),
	)
	if err := send(); err != nil {
		// Record the class and reply code only. The relay's text can quote the
		// recipient address or the account name, and spans outlive the mail.
		class, code := classifyError(err)
		span.SetAttributes(attribute.String("error.class", class))
		if code != 0 {
			span.SetAttributes(attribute.Int("smtp.code", code))
		}
		span.SetStatus(codes.Error, class)
		emailDeliveryCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("outcome", "error"),
			attribute.String("error_class", class),
			attribute.Int("attempt", attempt),
		))
		return err
	}
	emailDeliveryCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("outcome", "success"),
		attribute.Int("attempt", attempt),
	))
	return nil
}
