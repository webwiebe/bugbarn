---
title: Go SDK
description: Integrate BugBarn error tracking into Go applications.
---

# Go SDK

The Go SDK (`github.com/wiebe-xyz/bugbarn-go`) is a lightweight, zero-dependency client for reporting errors from Go services.

## Installation

```bash
go get github.com/wiebe-xyz/bugbarn-go
```

## Initialisation

Call `bugbarn.Init` early in your program. The SDK runs a background goroutine that batches and delivers events.

```go
import (
    "time"
    bugbarn "github.com/wiebe-xyz/bugbarn-go"
)

func main() {
    bugbarn.Init(bugbarn.Options{
        APIKey:      "your-api-key",
        Endpoint:    "https://bugbarn.example.com/api/v1/events",
        ProjectSlug: "my-service",        // optional -- routes events to a specific project
        Release:     "v1.2.3",            // optional -- tag events with a release version
        Environment: "production",        // optional -- tag events with an environment
        QueueSize:   256,                 // optional -- internal queue capacity (default 256)
    })
    defer bugbarn.Shutdown(2 * time.Second)

    // ...
}
```

### Options reference

| Field | Type | Default | Description |
|---|---|---|---|
| `APIKey` | `string` | required | Project API key |
| `Endpoint` | `string` | required | BugBarn ingest URL (`/api/v1/events`) |
| `ProjectSlug` | `string` | `""` | Route events to a named project |
| `Release` | `string` | `""` | Release identifier attached to every event |
| `Environment` | `string` | `""` | Environment name (e.g. `production`, `staging`) |
| `QueueSize` | `int` | `256` | Max queued events before drops |

## Capturing errors

### CaptureError

Report an `error` value. Returns `true` if the event was enqueued.

```go
err := doWork()
if err != nil {
    bugbarn.CaptureError(err)
}
```

### CaptureMessage

Report a plain string message.

```go
bugbarn.CaptureMessage("deployment started")
```

### Capture options

Both `CaptureError` and `CaptureMessage` accept variadic options:

```go
bugbarn.CaptureError(err,
    bugbarn.WithAttributes(map[string]any{
        "order_id": 42,
        "service":  "checkout",
    }),
    bugbarn.WithUser("u_123", "jane@example.com", "jane"),
)
```

| Option | Description |
|---|---|
| `WithAttributes(map[string]any)` | Attach arbitrary key-value metadata |
| `WithUser(id, email, username)` | Attach user context to the event |

## Panic recovery middleware

Wrap your HTTP handler to automatically capture panics. After capturing, the panic is re-raised so upstream middleware (or the Go runtime) can handle it.

```go
mux := http.NewServeMux()
mux.HandleFunc("/", handler)

wrapped := bugbarn.RecoverMiddleware(mux)
http.ListenAndServe(":8080", wrapped)
```

The middleware performs a best-effort flush (500ms timeout) before re-panicking, so the event is sent even if the process crashes.

## Flush and shutdown

### Flush

Drain all queued events within a timeout. Returns `true` if fully drained.

```go
drained := bugbarn.Flush(5 * time.Second)
```

### Shutdown

Flush remaining events and stop the background goroutine. Always call this before exiting.

```go
defer bugbarn.Shutdown(2 * time.Second)
```

`Shutdown` returns `true` if all events were delivered within the timeout.

## Full example

```go
package main

import (
    "errors"
    "fmt"
    "net/http"
    "os"
    "time"

    bugbarn "github.com/wiebe-xyz/bugbarn-go"
)

func main() {
    bugbarn.Init(bugbarn.Options{
        APIKey:      os.Getenv("BUGBARN_API_KEY"),
        Endpoint:    os.Getenv("BUGBARN_ENDPOINT"),
        Environment: "production",
        Release:     "v1.0.0",
    })
    defer bugbarn.Shutdown(2 * time.Second)

    // Manual capture
    bugbarn.CaptureError(errors.New("something went wrong"),
        bugbarn.WithAttributes(map[string]any{"service": "api"}),
    )

    // HTTP server with panic recovery
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, "OK")
    })

    http.ListenAndServe(":8080", bugbarn.RecoverMiddleware(mux))
}
```
