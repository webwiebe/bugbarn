package ingestproc

import (
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestStampEnvironmentOnlyFillsAMissingOne(t *testing.T) {
	t.Parallel()
	bare := event.Event{Resource: map[string]any{"service.name": "web"}}
	got := stampEnvironment(bare, "staging")
	if storage.EventEnvironment(got) != "staging" || got.Resource["service.name"] != "web" {
		t.Errorf("stamped event = %+v, want environment staging and the resource kept", got.Resource)
	}
	if _, touched := bare.Resource["deployment.environment"]; touched {
		t.Error("the caller's resource map was modified")
	}

	own := event.Event{Attributes: map[string]any{"environment": "production"}}
	if storage.EventEnvironment(stampEnvironment(own, "staging")) != "production" {
		t.Error("an event that names its own environment must keep it")
	}
	if storage.EventEnvironment(stampEnvironment(event.Event{}, "")) != "" {
		t.Error("no merge environment must stamp nothing")
	}
}
