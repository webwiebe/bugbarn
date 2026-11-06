package logparse

import (
	"errors"
	"testing"
)

func TestDecodeJSONBody(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{}`, `{"logs":null}`, `null`, `{"logs":"x"}`, `{"logs":{}}`, `{"logs":1}`} {
		if _, err := DecodeJSONBody([]byte(body)); !errors.Is(err, ErrNoLogsArray) {
			t.Errorf("%s: err = %v, want ErrNoLogsArray", body, err)
		}
	}
	for _, body := range []string{`not json`, `[{"message":"bare"}]`, `{"logs":[1]}`} {
		if _, err := DecodeJSONBody([]byte(body)); err == nil || errors.Is(err, ErrNoLogsArray) {
			t.Errorf("%s: err = %v, want a decode error", body, err)
		}
	}
	got, err := DecodeJSONBody([]byte(`{"logs": [{"message":"a"},{"message":"b"}]}`))
	if err != nil || len(got) != 2 || got[1]["message"] != "b" {
		t.Errorf("valid body: got %v, %v", got, err)
	}
	if got, err := DecodeJSONBody([]byte(`{"logs":[]}`)); err != nil || len(got) != 0 {
		t.Errorf("empty batch: got %v, %v, want no entries and no error", got, err)
	}
}

func TestIsNDJSON(t *testing.T) {
	t.Parallel()

	for ct, want := range map[string]bool{
		"application/x-ndjson":                true,
		"application/x-ndjson; charset=utf-8": true,
		"application/json":                    false,
		"":                                    false,
	} {
		if got := IsNDJSON(ct); got != want {
			t.Errorf("IsNDJSON(%q) = %v, want %v", ct, got, want)
		}
	}
}
