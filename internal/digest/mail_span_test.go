package digest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestClassifyMailError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantClass string
		wantCode  int
	}{
		{"relay busy", &textproto.Error{Code: 451, Msg: "4.7.1 too many connections"}, "temporary_rejection", 451},
		{"auth", &textproto.Error{Code: 535, Msg: "5.7.8 bad credentials for ops@example.com"}, "auth", 535},
		{"recipient rejected", &textproto.Error{Code: 550, Msg: "5.1.1 <jane@example.com> no such user"}, "permanent_rejection", 550},
		{"deadline", fmt.Errorf("dial: %w", context.DeadlineExceeded), "timeout", 0},
		{"net timeout", &net.OpError{Op: "read", Err: timeoutErr{}}, "timeout", 0},
		{"refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "connect", 0},
		{"unknown", errors.New("something odd"), "other", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, code := classifyMailError(tc.err)
			if class != tc.wantClass || code != tc.wantCode {
				t.Fatalf("classifyMailError = %q, %d; want %q, %d", class, code, tc.wantClass, tc.wantCode)
			}
		})
	}
}

func TestMailErrorTextCarriesNoRelayOutput(t *testing.T) {
	err := &MailError{Class: "permanent_rejection", Code: 550}
	if got := err.Error(); got != "send email failed: permanent_rejection (smtp 550)" {
		t.Fatalf("Error() = %q", got)
	}
	canceledErr := &MailError{Class: "other", cause: context.Canceled}
	if !errors.Is(canceledErr, context.Canceled) {
		t.Fatal("MailError hides context.Canceled from errors.Is")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// A failed send span says why it failed without carrying the relay's text,
// which can quote recipient addresses or credentials.
func TestSendMailTracedRecordsClassNotMessage(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	sendErr := &textproto.Error{Code: 550, Msg: "5.1.1 <jane@example.com> no such user"}
	got := sendMailTraced(context.Background(), "mail.example.com", 2, func() error { return sendErr })
	if !errors.Is(got, sendErr) {
		t.Fatalf("err = %v; want %v", got, sendErr)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d; want 1", len(spans))
	}
	span := spans[0]
	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range span.Attributes {
		attrs[kv.Key] = kv.Value
	}
	if attrs["error.class"].AsString() != "permanent_rejection" {
		t.Fatalf("error.class = %q", attrs["error.class"].AsString())
	}
	if attrs["smtp.code"].AsInt64() != 550 {
		t.Fatalf("smtp.code = %d", attrs["smtp.code"].AsInt64())
	}

	var dump strings.Builder
	dump.WriteString(span.Status.Description)
	for _, kv := range span.Attributes {
		dump.WriteString(kv.Value.String())
	}
	for _, ev := range span.Events {
		dump.WriteString(ev.Name)
		for _, kv := range ev.Attributes {
			dump.WriteString(kv.Value.String())
		}
	}
	if strings.Contains(dump.String(), "jane@example.com") {
		t.Fatalf("span carries the recipient address: %s", dump.String())
	}
}
