package logparse

import "testing"

func TestParseBodyJSON(t *testing.T) {
	body := []byte(`{"logs":[{"level":"error","msg":"boom","reqId":"x"},{"level":30,"msg":"ok"}]}`)
	entries := ParseBody(body, "application/json", 7)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].ProjectID != 7 || entries[0].Level != "error" || entries[0].LevelNum != 50 {
		t.Errorf("entry0 = %+v", entries[0])
	}
	if entries[0].Message != "boom" {
		t.Errorf("message = %q", entries[0].Message)
	}
	if entries[0].Data["reqId"] != "x" {
		t.Errorf("data not carried through: %+v", entries[0].Data)
	}
	if entries[1].Level != "info" || entries[1].LevelNum != 30 {
		t.Errorf("numeric level not mapped: %+v", entries[1])
	}
}

func TestParseBodyNDJSON(t *testing.T) {
	body := []byte("{\"level\":\"warn\",\"msg\":\"a\"}\n\n{\"level\":\"debug\",\"msg\":\"b\"}\n")
	entries := ParseBody(body, "application/x-ndjson; charset=utf-8", 1)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Level != "warn" || entries[1].Level != "debug" {
		t.Errorf("levels = %q, %q", entries[0].Level, entries[1].Level)
	}
}

func TestParseBodyInvalidReturnsNil(t *testing.T) {
	if entries := ParseBody([]byte("not json"), "application/json", 1); entries != nil {
		t.Errorf("expected nil for invalid body, got %v", entries)
	}
}

func TestLevelMinFromName(t *testing.T) {
	if got := LevelMinFromName("Error"); got != 50 {
		t.Errorf("LevelMinFromName(Error) = %d, want 50", got)
	}
	if got := LevelMinFromName("nope"); got != 0 {
		t.Errorf("LevelMinFromName(nope) = %d, want 0", got)
	}
}
