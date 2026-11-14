package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

type fakeMaps struct {
	blob  []byte
	err   error
	calls []string
}

func (f *fakeMaps) FindSourceMap(_ context.Context, release, dist, bundleURL string) ([]byte, error) {
	f.calls = append(f.calls, release+"|"+dist+"|"+bundleURL)
	return f.blob, f.err
}

const testMap = `{"version":3,"file":"app.min.js","sources":["src/app.ts"],"sourcesContent":["a\nb\nc"],"names":[],"mappings":"AAAA"}`

func jsEvent(frames ...event.StackFrame) event.Event {
	return event.Event{
		Attributes: map[string]any{"release": "1.2.3", "dist": "web"},
		Exception:  event.Exception{Stacktrace: frames},
	}
}

func TestSymbolicateEventResolvesMinifiedFrames(t *testing.T) {
	t.Parallel()
	maps := &fakeMaps{blob: []byte(testMap)}
	got := SymbolicateEvent(context.Background(), jsEvent(event.StackFrame{File: "https://x/app.min.js", Line: 1, Column: 0}), maps)

	frame := got.Exception.Stacktrace[0]
	if frame.OriginalFile != "src/app.ts" || frame.OriginalLine != 1 {
		t.Errorf("frame = %+v, want the original position", frame)
	}
	if len(maps.calls) != 1 || maps.calls[0] != "1.2.3|web|https://x/app.min.js" {
		t.Errorf("lookup = %v, want release, dist and bundle url", maps.calls)
	}
}

func TestSymbolicateEventSkipsWhatItCannotResolve(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		maps  *fakeMaps
		frame event.StackFrame
		calls int
	}{
		"native frame":    {&fakeMaps{blob: []byte(testMap)}, event.StackFrame{File: "main.go", Line: 1, Column: 1}, 0},
		"no position":     {&fakeMaps{blob: []byte(testMap)}, event.StackFrame{File: "a.js"}, 0},
		"lookup error":    {&fakeMaps{err: errors.New("db down")}, event.StackFrame{File: "a.js", Line: 1, Column: 1}, 1},
		"no stored map":   {&fakeMaps{}, event.StackFrame{File: "a.mjs", Line: 1, Column: 1}, 1},
		"unparseable map": {&fakeMaps{blob: []byte("{")}, event.StackFrame{File: "a.js", Line: 1, Column: 1}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			got := SymbolicateEvent(context.Background(), jsEvent(tc.frame), tc.maps)
			if got.Exception.Stacktrace[0].OriginalFile != "" {
				t.Errorf("frame resolved: %+v", got.Exception.Stacktrace[0])
			}
			if len(tc.maps.calls) != tc.calls {
				t.Errorf("lookups = %d, want %d", len(tc.maps.calls), tc.calls)
			}
		})
	}
}

func TestSymbolicateEventWithoutFramesReturnsTheEvent(t *testing.T) {
	t.Parallel()
	maps := &fakeMaps{}
	evt := event.Event{Message: "boom"}
	if got := SymbolicateEvent(context.Background(), evt, maps); got.Message != "boom" || len(maps.calls) != 0 {
		t.Errorf("got %+v with %d lookups", got, len(maps.calls))
	}
}

func TestReleaseAndDistFallBackToTheResource(t *testing.T) {
	t.Parallel()
	evt := event.Event{Resource: map[string]any{"release": "r9", "dist": "d9"}}
	if eventRelease(evt) != "r9" || eventDist(evt) != "d9" {
		t.Errorf("resource fallback = %q %q", eventRelease(evt), eventDist(evt))
	}
	if eventRelease(event.Event{}) != "" || eventDist(event.Event{}) != "" {
		t.Error("absent release and dist must read as empty")
	}
}
