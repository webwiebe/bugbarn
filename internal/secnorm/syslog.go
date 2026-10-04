package secnorm

import (
	"regexp"
	"strings"
)

// sshd and sudo message patterns. OpenSSH words these the same on Linux and
// macOS; PAM-backed macOS sshd adds the "error: PAM:" form.
var (
	sshFailed   = regexp.MustCompile(`^Failed (\S+) for (?:invalid user )?(\S*) from (\S+) port \d+`)
	sshInvalid  = regexp.MustCompile(`^Invalid user (\S*) from (\S+)(?: port \d+)?`)
	sshAccepted = regexp.MustCompile(`^Accepted (\S+) for (\S+) from (\S+) port \d+`)
	sshMaxAuth  = regexp.MustCompile(`^(?:error: )?maximum authentication attempts exceeded for (?:invalid user )?(\S*) from (\S+)`)
	sshPreauth  = regexp.MustCompile(
		`^(?:Connection closed|Disconnected) by (?:authenticating |invalid )?user (\S*) (\S+) port \d+ \[preauth\]`)
	sshPAM      = regexp.MustCompile(`^error: PAM: authentication error for (?:illegal user )?(\S*) from (\S+)`)
	sudoCommand = regexp.MustCompile(`^\s*(\S+) : .*COMMAND=(.*)$`)
	sudoFailure = regexp.MustCompile(`^pam_unix\(sudo:auth\): authentication failure;.*ruser=(\S*)`)
	sudoBadPass = regexp.MustCompile(`^\s*(\S+) : \d+ incorrect password attempts?`)
)

// syslogLine maps a journald (or plain syslog) event. Vector's journald source
// keeps the journal field names (SYSLOG_IDENTIFIER, _COMM) next to `message`.
func syslogLine(base Record, ev map[string]any) (Record, bool) {
	ident := firstNonEmpty(str(ev, "SYSLOG_IDENTIFIER"), str(ev, "_COMM"), str(ev, "appname"))
	msg := firstNonEmpty(str(ev, "message"), str(ev, "MESSAGE"))
	base.Host = firstNonEmpty(base.Host, str(ev, "_HOSTNAME"))
	return parseAuthMessage(base, ident, msg), true
}

// macOSLog maps a `log stream --style ndjson` line.
func macOSLog(base Record, in map[string]any) (Record, bool) {
	if in == nil {
		return base, false
	}
	proc := str(in, "processImagePath")
	if i := strings.LastIndexByte(proc, '/'); i >= 0 {
		proc = proc[i+1:]
	}
	rec := parseAuthMessage(base, proc, str(in, "eventMessage"))
	if rec.Source == "journald" {
		rec.Source = "macos"
	}
	return rec, true
}

// parseAuthMessage classifies an sshd or sudo message. Anything else from the
// journal is kept as kind "other" under source "journald".
func parseAuthMessage(base Record, ident, msg string) Record {
	base.Message = truncate(msg, 1024)
	switch {
	case strings.HasPrefix(ident, "sshd"):
		base.Source = "sshd"
		return classifySSH(base, msg)
	case ident == "sudo":
		base.Source = "sudo"
		return classifySudo(base, msg)
	}
	base.Source = "journald"
	base.Kind = "other"
	base.Action = ident
	return base
}

func classifySSH(r Record, msg string) Record {
	if m := sshFailed.FindStringSubmatch(msg); m != nil {
		r.Kind, r.Action, r.User, r.SrcIP = "auth_failure", m[1], m[2], clientIP(m[3])
		if strings.Contains(msg, "for invalid user ") {
			r.Kind = "invalid_user"
		}
		return r
	}
	if m := sshAccepted.FindStringSubmatch(msg); m != nil {
		r.Kind, r.Action, r.User, r.SrcIP = "auth_success", m[1], m[2], clientIP(m[3])
		return r
	}
	for _, p := range []struct {
		re   *regexp.Regexp
		kind string
	}{{sshInvalid, "invalid_user"}, {sshMaxAuth, "auth_failure"}, {sshPAM, "auth_failure"}, {sshPreauth, "preauth_disconnect"}} {
		if m := p.re.FindStringSubmatch(msg); m != nil {
			r.Kind, r.User, r.SrcIP = p.kind, m[1], clientIP(m[2])
			return r
		}
	}
	r.Kind = "other"
	return r
}

func classifySudo(r Record, msg string) Record {
	switch m := sudoCommand.FindStringSubmatch(msg); {
	case m != nil && strings.Contains(msg, "incorrect password"):
		r.Kind, r.User = "sudo_failure", m[1]
	case m != nil:
		r.Kind, r.User, r.Action = "sudo", m[1], truncate(strings.TrimSpace(m[2]), 256)
	default:
		r.Kind = "other"
		if f := sudoFailure.FindStringSubmatch(msg); f != nil {
			r.Kind, r.User = "sudo_failure", f[1]
		} else if f := sudoBadPass.FindStringSubmatch(msg); f != nil {
			r.Kind, r.User = "sudo_failure", f[1]
		}
	}
	return r
}
