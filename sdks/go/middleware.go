package bugbarn

import (
	"net/http"
	"runtime/debug"
	"time"
)

// RecoverMiddleware wraps an http.Handler to capture panics as BugBarn events.
// After capturing, the panic is re-raised so upstream middleware can handle it.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				env := buildPanicEnvelope(rec, stack)
				mu.Lock()
				t := tp
				mu.Unlock()
				if t != nil {
					t.enqueue(env)
					// Best-effort flush with short timeout so the event is sent
					// before the process potentially crashes.
					t.flush(500 * time.Millisecond)
				}
				panic(rec) // re-raise
			}
		}()
		next.ServeHTTP(w, r)
	})
}
