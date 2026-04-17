package bugbarn

import (
	"sync"
	"time"
)

// Options configures the SDK.
type Options struct {
	APIKey      string
	Endpoint    string
	ProjectSlug string
	Release     string
	Environment string
	QueueSize   int // default 256
}

// CaptureOption configures a single capture call.
type CaptureOption func(*captureOpts)

type captureOpts struct {
	attributes  map[string]any
	user        *userContext
	release     string
	environment string
}

// WithAttributes attaches key-value metadata to a captured event.
func WithAttributes(attrs map[string]any) CaptureOption {
	return func(o *captureOpts) { o.attributes = attrs }
}

// WithUser attaches user context to a captured event.
func WithUser(id, email, username string) CaptureOption {
	return func(o *captureOpts) {
		o.user = &userContext{ID: id, Email: email, Username: username}
	}
}

var (
	mu   sync.Mutex
	tp   *transport
	opts Options
)

// Init initialises the SDK. Safe to call multiple times; re-initialises.
func Init(o Options) {
	mu.Lock()
	defer mu.Unlock()
	if tp != nil {
		tp.shutdown(2 * time.Second) // drain old transport
	}
	if o.QueueSize <= 0 {
		o.QueueSize = 256
	}
	opts = o
	tp = newTransport(o.APIKey, o.Endpoint, o.ProjectSlug, o.QueueSize)
}

// CaptureError captures an error and enqueues it for delivery.
func CaptureError(err error, options ...CaptureOption) bool {
	mu.Lock()
	t := tp
	mu.Unlock()
	if t == nil || err == nil {
		return false
	}
	co := applyOpts(options)
	co.release = opts.Release
	co.environment = opts.Environment
	return t.enqueue(buildEnvelope(err, co))
}

// CaptureMessage captures a plain string message.
func CaptureMessage(msg string, options ...CaptureOption) bool {
	mu.Lock()
	t := tp
	mu.Unlock()
	if t == nil {
		return false
	}
	co := applyOpts(options)
	co.release = opts.Release
	co.environment = opts.Environment
	return t.enqueue(buildMessageEnvelope(msg, co))
}

// Flush waits for queued events to drain within the timeout. Returns true if fully drained.
func Flush(timeout time.Duration) bool {
	mu.Lock()
	t := tp
	mu.Unlock()
	if t == nil {
		return true
	}
	return t.flush(timeout)
}

// Shutdown flushes and stops the background goroutine.
func Shutdown(timeout time.Duration) bool {
	mu.Lock()
	t := tp
	tp = nil
	mu.Unlock()
	if t == nil {
		return true
	}
	return t.shutdown(timeout)
}

func applyOpts(options []CaptureOption) captureOpts {
	co := captureOpts{attributes: make(map[string]any)}
	for _, o := range options {
		o(&co)
	}
	return co
}
