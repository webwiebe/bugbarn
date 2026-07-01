package alert

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// capturedMail is one message a fakeSMTPServer received over the wire.
type capturedMail struct {
	from string
	to   []string
	data string
}

// fakeSMTPServer is a minimal SMTP server (enough of the protocol for
// net/smtp.SendMail) that captures delivered messages. It advertises AUTH
// PLAIN so the client's auth step succeeds, and accepts any credentials.
type fakeSMTPServer struct {
	ln       net.Listener
	received chan capturedMail
}

func newFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{ln: ln, received: make(chan capturedMail, 8)}
	go s.serve()
	return s
}

func (s *fakeSMTPServer) addr() (host string, port int) {
	a := s.ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func (s *fakeSMTPServer) close() { _ = s.ln.Close() }

func (s *fakeSMTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := func(format string, args ...any) { fmt.Fprintf(conn, format+"\r\n", args...) }

	w("220 fake ESMTP")
	var mail capturedMail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			w("250-fake greets you")
			w("250 AUTH PLAIN")
		case strings.HasPrefix(upper, "AUTH"):
			w("235 2.7.0 accepted")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			mail.from = strings.TrimPrefix(line[len("MAIL FROM:"):], "")
			w("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			mail.to = append(mail.to, strings.TrimSpace(line[len("RCPT TO:"):]))
			w("250 ok")
		case upper == "DATA":
			w("354 end data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				dl, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if dl == ".\r\n" || dl == ".\n" {
					break
				}
				body.WriteString(dl)
			}
			mail.data = body.String()
			w("250 ok queued")
			s.received <- mail
			mail = capturedMail{}
		case upper == "QUIT":
			w("221 bye")
			return
		case upper == "RSET":
			mail = capturedMail{}
			w("250 ok")
		default:
			w("250 ok")
		}
	}
}

// TestEvaluator_AdminEmailFiresOverSMTP runs the real production path
// (Bus -> HandleEvent -> notifyAdmin -> Deliverer.Fire -> digest.DeliverEmail
// -> net/smtp) against a live in-process SMTP server, and asserts that a new
// issue and a regression each deliver a real email to the admin address.
func TestEvaluator_AdminEmailFiresOverSMTP(t *testing.T) {
	t.Parallel()

	srv := newFakeSMTPServer(t)
	defer srv.close()
	host, port := srv.addr()

	mailCfg := digest.MailConfig{
		Enabled: true,
		Host:    host,
		Port:    port,
		From:    "bugbarn@localhost",
	}
	deliverer := NewDeliverer(mailCfg)
	if !deliverer.EmailConfigured() {
		t.Fatal("expected EmailConfigured() to be true with host + enabled")
	}

	const admin = "admin@example.com"
	evaluator := NewEvaluator(newFakeRepo(nil), deliverer, "https://bugbarn.example.com", admin, slog.Default())

	var bus domainevents.Bus
	bus.Subscribe(evaluator.HandleEvent)

	bus.Publish(domainevents.IssueCreated{
		Issue:     storage.Issue{ID: "issue-000001", Title: "NewBoom: kaboom in prod"},
		ProjectID: 42,
	})
	bus.Publish(domainevents.IssueRegressed{
		Issue:     storage.Issue{ID: "issue-000002", Title: "RegressBoom: it came back"},
		ProjectID: 42,
	})

	mails := make([]capturedMail, 0, 2)
	deadline := time.After(10 * time.Second)
	for len(mails) < 2 {
		select {
		case m := <-srv.received:
			mails = append(mails, m)
		case <-deadline:
			t.Fatalf("timed out: only %d/2 emails delivered over SMTP", len(mails))
		}
	}

	var sawNew, sawRegression bool
	for _, m := range mails {
		if !strings.Contains(strings.Join(m.to, ","), admin) {
			t.Errorf("email not addressed to admin: rcpt=%v", m.to)
		}
		if !strings.Contains(m.data, admin) {
			t.Errorf("message body/headers missing admin recipient: %q", m.data)
		}
		switch {
		case strings.Contains(m.data, "New issue created") && strings.Contains(m.data, "NewBoom"):
			sawNew = true
		case strings.Contains(m.data, "Issue regressed") && strings.Contains(m.data, "RegressBoom"):
			sawRegression = true
		}
	}
	if !sawNew {
		t.Error("did not receive the new-issue admin email")
	}
	if !sawRegression {
		t.Error("did not receive the regression admin email")
	}

	t.Logf("delivered %d real SMTP emails to %s (new issue + regression)", len(mails), admin)
}
