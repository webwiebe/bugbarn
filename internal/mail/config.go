package mail

import netmail "net/mail"

// Config holds SMTP settings. All fields are optional: when Enabled is
// false (or Host/To are empty) the mailer is a no-op. Env vars follow the
// same naming convention as rapid-root: SMTP_HOST, SMTP_PORT, SMTP_USER,
// SMTP_PASS, SMTP_FROM, SMTP_FROM_NAME, SMTP_REPLY_TO, with
// BUGBARN_DIGEST_ENABLED and BUGBARN_DIGEST_TO as the BugBarn-specific opt-in
// controls.
//
// From is the mailbox we send as, and is what goes on the SMTP envelope; the
// account BugBarn authenticates with is send-only, so FromName gives the
// message a human label and ReplyTo points replies at a mailbox somebody
// actually reads.
type Config struct {
	Enabled  bool
	Host     string
	Port     int
	User     string
	Pass     string
	From     string
	FromName string
	ReplyTo  string
	To       string
}

// Active reports whether SMTP delivery is configured and enabled.
func (m Config) Active() bool {
	return m.Enabled && m.Host != "" && m.To != ""
}

// formatAddress renders an address header value. Without a display name it
// stays the bare address, which is exactly what every deployment sent before
// FromName existed; with one it becomes `"Name" <addr>`, RFC 2047-encoded when
// the name is not plain ASCII. The envelope sender is always the bare address,
// so this only ever affects what the recipient sees.
func formatAddress(name, addr string) string {
	if name == "" {
		return addr
	}
	return (&netmail.Address{Name: name, Address: addr}).String()
}
