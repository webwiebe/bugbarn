package secnorm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// traefik maps a Traefik JSON access-log line. Behind Cloudflare, ClientHost
// is a Cloudflare edge address, so the real client comes from the
// Cf-Connecting-Ip header (kept with Traefik's accesslog.fields.headers), then
// X-Forwarded-For when the peer is a trusted gateway, then ClientHost.
func traefik(base Record, in map[string]any, opts Options) (Record, bool) {
	if in == nil {
		return base, false
	}
	base.Source = "traefik"
	base.Kind = "http"
	base.Method = str(in, "RequestMethod")
	base.Path = str(in, "RequestPath")
	base.RequestHost = str(in, "RequestHost")
	base.Status = num(in, "DownstreamStatus")
	peer := firstNonEmpty(clientIP(str(in, "ClientHost")), clientIP(str(in, "ClientAddr")))
	base.SrcIP = firstNonEmpty(
		clientIP(str(in, "request_Cf-Connecting-Ip")),
		forwardedClient(str(in, "request_X-Forwarded-For"), peer, opts.TrustedGateways),
		peer,
	)
	base.User = strings.Trim(str(in, "ClientUsername"), "-")
	base.Action = str(in, "RouterName")
	if t, ok := parseTime(str(in, "StartUTC")); ok {
		base.TS = t
	}
	base.Message = fmt.Sprintf("%s %s%s %d", base.Method, str(in, "RequestHost"), base.Path, base.Status) + callerSuffix(in)
	return base, true
}

// callerSuffix names who sent a request, from the request headers Traefik
// keeps (accesslog.fields.headers): the user agent and the project slug, so a
// burst of rejected requests can be traced to its sender. Headers Traefik does
// not log are left out. The API key header is never kept in access logs; the
// key fingerprint comes from the ingest handler's "ingest api key denied"
// warning (internal/ingest/denied.go).
func callerSuffix(in map[string]any) string {
	var b strings.Builder
	if ua := truncate(str(in, "request_User-Agent"), 120); ua != "" {
		b.WriteString(" ua=" + ua)
	}
	if p := truncate(str(in, "request_X-Bugbarn-Project"), 64); p != "" {
		b.WriteString(" project=" + p)
	}
	return b.String()
}

// k8sAudit maps a Kubernetes audit.k8s.io/v1 Event.
func k8sAudit(base Record, in map[string]any) (Record, bool) {
	if in == nil {
		return base, false
	}
	// Only the final stage carries the response code; earlier stages would
	// count every request twice.
	if stage := str(in, "stage"); stage != "" && stage != "ResponseComplete" && stage != "Panic" {
		return base, false
	}
	obj := sub(in, "objectRef")
	base.Source = "k8saudit"
	base.Kind = firstNonEmpty(str(obj, "resource"), "nonresource")
	base.Action = str(in, "verb")
	base.Method = base.Action
	base.User = str(sub(in, "user"), "username")
	base.Path = str(in, "requestURI")
	base.Status = num(sub(in, "responseStatus"), "code")
	if ips, ok := in["sourceIPs"].([]any); ok && len(ips) > 0 {
		base.SrcIP = clientIP(fmt.Sprint(ips[0]))
	}
	if t, ok := parseTime(str(in, "stageTimestamp")); ok {
		base.TS = t
	}
	target := str(obj, "name")
	if ns := str(obj, "namespace"); ns != "" {
		target = ns + "/" + target
	}
	base.Message = strings.TrimSpace(fmt.Sprintf("%s %s %s by %s", base.Action, base.Kind, target, base.User))
	return base, true
}

// cloudflare maps one firewallEventsAdaptive node (see internal/cfpoller).
func cloudflare(base Record, in map[string]any) (Record, bool) {
	if in == nil {
		return base, false
	}
	base.Source = "cloudflare"
	base.Kind = firstNonEmpty(str(in, "source"), "firewall")
	base.Action = str(in, "action")
	base.SrcIP = clientIP(str(in, "clientIP"))
	base.Method = str(in, "clientRequestHTTPMethodName")
	base.Path = str(in, "clientRequestPath")
	base.Status = num(in, "edgeResponseStatus")
	if t, ok := parseTime(str(in, "datetime")); ok {
		base.TS = t
	}
	host := str(in, "clientRequestHTTPHost")
	base.Host = firstNonEmpty(host, base.Host)
	base.RequestHost = host
	base.Message = strings.TrimSpace(fmt.Sprintf("%s %s %s%s (%s, rule %s)",
		base.Action, base.Method, host, base.Path, str(in, "clientCountryName"), str(in, "ruleId")))
	return base, true
}

// Cloudflare normalizes one firewallEventsAdaptive node as returned by the
// GraphQL Analytics API (internal/cfpoller polls it). It shares the field
// mapping with Vector-shipped Cloudflare events. now is the fallback time for
// a node without a parsable datetime.
func Cloudflare(node map[string]any, now time.Time) (Record, bool) {
	raw, _ := json.Marshal(node)
	base := Record{TS: now.UTC(), Raw: truncate(string(raw), MaxRawBytes)}
	return cloudflare(base, node)
}
