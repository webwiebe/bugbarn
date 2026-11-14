package mail

import (
	"context"
	"errors"
	"testing"
)

func TestConfigActive(t *testing.T) {
	full := Config{Enabled: true, Host: "smtp.example.com", To: "ops@example.com"}
	if !full.Active() {
		t.Fatal("complete config should be active")
	}
	for name, c := range map[string]Config{
		"disabled": {Host: "smtp.example.com", To: "ops@example.com"},
		"no host":  {Enabled: true, To: "ops@example.com"},
		"no to":    {Enabled: true, Host: "smtp.example.com"},
	} {
		if c.Active() {
			t.Errorf("%s: config should be inactive", name)
		}
	}
}

func TestErrorMessageAndUnwrap(t *testing.T) {
	if got := (&Error{Class: "auth", Code: 535}).Error(); got != "send email failed: auth (smtp 535)" {
		t.Fatalf("message with code = %q", got)
	}
	e := &Error{Class: "timeout", cause: context.DeadlineExceeded}
	if got := e.Error(); got != "send email failed: timeout" {
		t.Fatalf("message without code = %q", got)
	}
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("cause should unwrap")
	}
}
