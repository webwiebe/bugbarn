# Go SDK

Package: `github.com/wiebe-xyz/bugbarn-go`

## Installation

```bash
go get github.com/wiebe-xyz/bugbarn-go
```

## Initialisation

Call `bb.Init` once at application startup, before any errors can occur. It is safe to call multiple times; a second call drains the previous transport and starts a fresh one.

```go
import (
    "time"
    bb "github.com/wiebe-xyz/bugbarn-go"
)

func main() {
    bb.Init(bb.Options{
        APIKey:      "your-api-key",      // required
        Endpoint:    "https://bugbarn.example.com", // required: BugBarn server base URL
        ProjectSlug: "my-app",            // optional: routes events to this project
        Release:     "v1.2.3",           // optional: tag events with a release version
        Environment: "production",        // optional: tag events with an environment
        QueueSize:   256,                 // optional: in-memory queue capacity (default 256)
    })
    defer bb.Shutdown(5 * time.Second)

    // ... rest of main
}
```

### Options reference

| Option | Required | Default | Description |
|---|---|---|---|
| `APIKey` | Yes | — | API key with `ingest` scope |
| `Endpoint` | Yes | — | Base URL of your BugBarn server |
| `ProjectSlug` | No | `""` | Sends `X-BugBarn-Project` header on every request |
| `Release` | No | `""` | Attached to every event as the release version |
| `Environment` | No | `""` | Attached to every event as the environment name |
| `QueueSize` | No | `256` | In-memory channel buffer. When full, new events are dropped silently (see [Queue behaviour](#queue-behaviour)) |

## Capturing errors

```go
err := doSomething()
if err != nil {
    bb.CaptureError(err)
}
```

With extra context:

```go
bb.CaptureError(err,
    bb.WithAttributes(map[string]any{
        "order_id": orderID,
        "http.route": "/api/orders",
    }),
    bb.WithUser(userID, userEmail, username),
)
```

`CaptureError` returns `false` if the SDK has not been initialised, `err` is nil, or the queue is full.

## Capturing messages

Use `CaptureMessage` for non-error events — informational notices, warnings, or any string you want to send without an associated Go `error` value.

```go
bb.CaptureMessage("payment gateway timeout, falling back to retry queue",
    bb.WithAttributes(map[string]any{
        "gateway": "stripe",
        "timeout_ms": 3000,
    }),
)
```

## Attaching user context

`bb.WithUser` attaches a user to a single capture call. All three fields are optional; pass an empty string for any you do not have.

```go
bb.CaptureError(err, bb.WithUser("usr_abc123", "alice@example.com", "alice"))
```

## Attaching custom attributes

`bb.WithAttributes` accepts any `map[string]any`. Values are attached to the event under `attributes` and are searchable in BugBarn.

```go
bb.CaptureError(err, bb.WithAttributes(map[string]any{
    "tenant_id":  tenantID,
    "queue_name": "invoices",
    "retries":    3,
}))
```

`WithAttributes` and `WithUser` can be combined in the same call:

```go
bb.CaptureError(err,
    bb.WithAttributes(map[string]any{"order_id": orderID}),
    bb.WithUser(userID, "", ""),
)
```

## HTTP middleware

`bb.RecoverMiddleware` wraps any `http.Handler` and captures panics as BugBarn error events. After capturing, the panic is re-raised so that any upstream recovery middleware (e.g. your framework's error handler) can still handle it normally.

```go
mux := http.NewServeMux()
mux.HandleFunc("/api/orders", handleOrders)

// Place RecoverMiddleware at the outermost layer so it catches panics
// from all handlers beneath it.
handler := bb.RecoverMiddleware(mux)

http.ListenAndServe(":8080", handler)
```

The middleware does a brief best-effort flush (500 ms) immediately after capturing a panic so the event is delivered before the process potentially crashes.

## Shutdown

Always call `bb.Shutdown` (or `bb.Flush`) before your process exits. Without it, events that are in the queue but not yet sent will be lost.

```go
func main() {
    bb.Init(bb.Options{ /* ... */ })
    defer bb.Shutdown(5 * time.Second)

    // application logic
}
```

`bb.Shutdown` closes the background goroutine and waits up to the given timeout for the queue to drain. It returns `true` if all events were delivered, `false` if it timed out.

`bb.Flush` waits for the queue to drain without stopping the SDK — useful in long-running processes where you want to ensure delivery before a checkpoint.

```go
// Ensure events are sent before a graceful restart
bb.Flush(2 * time.Second)
```

## Queue behaviour

The SDK maintains a fixed-size in-memory channel between capture calls and the background delivery goroutine. The behaviour under load:

- Events are enqueued **without blocking**. If the channel is full, the new event is dropped and `CaptureError`/`CaptureMessage` returns `false`.
- The default queue size of 256 is sufficient for the vast majority of applications. Increase `QueueSize` only if you expect sustained bursts of more than 256 errors between delivery cycles.
- Network errors during delivery are logged silently; the SDK never crashes your application.

## Self-reporting pattern

BugBarn uses the Go SDK to report its own errors — a pattern worth adopting for any service that manages BugBarn infrastructure. Configure it via environment variables:

```bash
BUGBARN_SELF_ENDPOINT=https://bugbarn.example.com
BUGBARN_SELF_API_KEY=ingest-key-for-self-project
```

The application reads these at startup and initialises a second SDK instance pointed at a dedicated project. This gives you visibility into BugBarn's own health without polluting your application projects.

## Common patterns

### Error wrapper in a service layer

Wrap domain operations so that all errors are reported in one place rather than at every call site:

```go
func (s *OrderService) CreateOrder(ctx context.Context, req CreateOrderRequest) (*Order, error) {
    order, err := s.repo.Insert(ctx, req)
    if err != nil {
        bb.CaptureError(err, bb.WithAttributes(map[string]any{
            "customer_id": req.CustomerID,
            "http.route":  "/api/orders",
        }))
        return nil, err
    }
    return order, nil
}
```

### Panic recovery in an HTTP handler

If you cannot use `RecoverMiddleware` globally (e.g. you need handler-level recovery with a custom response), recover inline:

```go
func handleCheckout(w http.ResponseWriter, r *http.Request) {
    defer func() {
        if rec := recover(); rec != nil {
            bb.CaptureMessage(fmt.Sprintf("panic in checkout handler: %v", rec),
                bb.WithAttributes(map[string]any{"http.route": r.URL.Path}),
            )
            http.Error(w, "internal server error", http.StatusInternalServerError)
        }
    }()

    // handler logic that may panic
}
```

For most cases, prefer `RecoverMiddleware` — it captures a proper stack trace automatically.
