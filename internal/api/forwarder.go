package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/ingestresp"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// WriteForwarder proxies write requests to an upstream writer instance
// as part of the CQRS read/write split.
type WriteForwarder struct {
	writerURL string
	client    *http.Client
}

// NewWriteForwarder creates a forwarder that proxies requests to the given writer URL.
func NewWriteForwarder(writerURL string) *WriteForwarder {
	return &WriteForwarder{
		writerURL: writerURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
			// The default transport keeps 2 idle connections per host, so a burst
			// of ~30 concurrent writes dials ~28 fresh connections every time.
			Transport: forwarderTransport(),
		},
	}
}

func forwarderTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 64
	t.MaxIdleConns = 128
	return t
}

// maxReplayableBody bounds the bodies the forwarder buffers so the transport
// can replay them.
const maxReplayableBody = 4 << 20

// replayableBody returns body as a rewindable reader when it is small. The
// transport only retries a request that failed on a reused keep-alive
// connection (the writer closed it while idle) when it can send the body
// again; a streamed body turned that race into a 502 for a request the writer
// never saw. A body over the limit, or one that fails to read, is passed
// through as a stream.
func replayableBody(body io.ReadCloser) io.Reader {
	if body == nil || body == http.NoBody {
		return nil
	}
	buf, err := io.ReadAll(io.LimitReader(body, maxReplayableBody+1))
	if err != nil || len(buf) > maxReplayableBody {
		return io.MultiReader(bytes.NewReader(buf), body)
	}
	return bytes.NewReader(buf)
}

// Forward proxies the incoming request to the upstream writer and streams
// the response back to the client. On connection failure it returns 502.
func (f *WriteForwarder) Forward(w http.ResponseWriter, r *http.Request) {
	f.forward(w, r, r.Context())
}

// ForwardDetached proxies a request whose write is worth finishing after the
// client hangs up. The upstream call ignores the client's cancellation and runs
// under its own deadline, so a stalled writer holds this handler for at most
// timeout and starts no goroutine of its own. Use it only for writes that are
// safe to apply late and twice, such as a monitor check-in.
func (f *WriteForwarder) ForwardDetached(w http.ResponseWriter, r *http.Request, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer cancel()
	f.forward(w, r, ctx)
}

func (f *WriteForwarder) forward(w http.ResponseWriter, r *http.Request, parent context.Context) {
	upstreamURL := f.writerURL + r.URL.RequestURI()

	ctx, span := tracing.Tracer().Start(parent, "forwarder.Forward",
		trace.WithAttributes(
			attribute.String("http.target", upstreamURL),
			attribute.String("http.method", r.Method),
		),
	)
	defer span.End()
	r = r.WithContext(ctx)

	upstreamReq, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, replayableBody(r.Body))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		http.Error(w, "upstream writer unavailable", http.StatusBadGateway)
		return
	}

	// Copy all request headers.
	for key, vals := range r.Header {
		for _, v := range vals {
			upstreamReq.Header.Add(key, v)
		}
	}

	// The transport replays a POST only when the request carries an
	// Idempotency-Key entry. An empty entry is never sent, and it limits the
	// replay to the case that matters: a reused connection that failed before
	// the writer returned any byte, i.e. one the writer closed while idle.
	if _, ok := upstreamReq.Header["Idempotency-Key"]; !ok && upstreamReq.GetBody != nil {
		upstreamReq.Header["Idempotency-Key"] = nil
	}

	// Set Host to the writer's host, not the original.
	parsed, err := url.Parse(f.writerURL)
	if err == nil {
		upstreamReq.Host = parsed.Host
	}

	resp, err := f.client.Do(upstreamReq)
	if err != nil {
		class, neverSent := classifyForwardError(err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("forward.error_class", class))
		span.SetStatus(codes.Error, err.Error())
		slog.Warn("forward to writer failed", "error_class", class, "error", err,
			"method", r.Method, "path", r.URL.Path, "writer_may_have_acted", !neverSent)
		if neverSent {
			// The request never left this pod, so the SDK can retry without
			// duplicating anything.
			ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
			return
		}
		http.Error(w, "upstream writer unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= 500 {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	relayResponse(w, resp)
}

// relayResponse copies the writer's answer to the client.
func relayResponse(w http.ResponseWriter, resp *http.Response) {
	// Copy response headers, excluding CORS headers that the outer ServeHTTP
	// already set. Copying them would produce duplicates, and the Fetch spec
	// requires exactly one Access-Control-Allow-Origin value.
	corsHeaders := map[string]bool{
		"Access-Control-Allow-Origin":      true,
		"Access-Control-Allow-Headers":     true,
		"Access-Control-Allow-Methods":     true,
		"Access-Control-Allow-Credentials": true,
		"Access-Control-Expose-Headers":    true,
		"Access-Control-Max-Age":           true,
	}
	for key, vals := range resp.Header {
		if corsHeaders[key] {
			continue
		}
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	// Stream the body without buffering.
	io.Copy(w, resp.Body)
}

// classifyForwardError names why a forward failed and whether the request cannot
// have reached the writer. Only a failed dial qualifies: once a connection
// exists the request may have been written and acted on, so a retry could
// duplicate an ingest event.
func classifyForwardError(err error) (class string, neverSent bool) {
	var opErr *net.OpError
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &opErr) && opErr.Op == "dial", errors.As(err, &dnsErr):
		return "dial", true
	case errors.Is(err, context.Canceled):
		return "canceled", false
	case errors.Is(err, context.DeadlineExceeded), isNetTimeout(err):
		return "timeout", false
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return "reset", false
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof", false
	}
	return "other", false
}

func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
