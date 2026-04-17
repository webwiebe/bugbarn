package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	bugbarn "github.com/wiebe-xyz/bugbarn-go"
)

func main() {
	endpoint := os.Getenv("BUGBARN_ENDPOINT")
	apiKey := os.Getenv("BUGBARN_API_KEY")
	if endpoint == "" || apiKey == "" {
		log.Fatal("Set BUGBARN_ENDPOINT and BUGBARN_API_KEY")
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
	fmt.Println("Captured error")

	// Panic recovery via middleware.
	handler := bugbarn.RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("example panic")
	}))
	_ = handler // In real usage: http.ListenAndServe(":8080", handler)
	fmt.Println("Done — flushing...")
}
