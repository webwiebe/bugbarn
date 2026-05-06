package api

import (
	"io"
	"net/http"
	"net/url"
	"time"
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
		},
	}
}

// Forward proxies the incoming request to the upstream writer and streams
// the response back to the client. On connection failure it returns 502.
func (f *WriteForwarder) Forward(w http.ResponseWriter, r *http.Request) {
	upstreamURL := f.writerURL + r.URL.RequestURI()

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "upstream writer unavailable", http.StatusBadGateway)
		return
	}

	// Copy all request headers.
	for key, vals := range r.Header {
		for _, v := range vals {
			upstreamReq.Header.Add(key, v)
		}
	}

	// Set Host to the writer's host, not the original.
	parsed, err := url.Parse(f.writerURL)
	if err == nil {
		upstreamReq.Host = parsed.Host
	}

	resp, err := f.client.Do(upstreamReq)
	if err != nil {
		http.Error(w, "upstream writer unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy all response headers.
	for key, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	// Stream the body without buffering.
	io.Copy(w, resp.Body)
}
