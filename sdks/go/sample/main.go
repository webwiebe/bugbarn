package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	bugbarn "github.com/wiebe-xyz/bugbarn-go"
)

func main() {
	if err := run(os.Getenv, os.Stdout); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

// run is the whole sample, with its environment and output injected so a test
// can drive it.
func run(getenv func(string) string, out io.Writer) error {
	endpoint := getenv("BUGBARN_ENDPOINT")
	apiKey := getenv("BUGBARN_API_KEY")
	if endpoint == "" || apiKey == "" {
		return errors.New("BUGBARN_ENDPOINT and BUGBARN_API_KEY must be set")
	}

	bugbarn.Init(bugbarn.Options{
		APIKey:      apiKey,
		Endpoint:    endpoint,
		Environment: "local",
	})
	defer bugbarn.Shutdown(2 * time.Second)

	// Manual capture.
	bugbarn.CaptureError(errors.New("something went wrong in Go"),
		bugbarn.WithAttributes(map[string]any{"service": "go-sample"}))
	_, _ = fmt.Fprintln(out, "Captured error")

	// Panic recovery via middleware.
	handler := bugbarn.RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("example panic")
	}))
	_ = handler // In real usage: http.ListenAndServe(":8080", handler)
	_, _ = fmt.Fprintln(out, "Done — flushing...")
	return nil
}
