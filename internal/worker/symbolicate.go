package worker

import (
	"context"
	"log"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/sourcemap"
)

// SourceMapStore is the subset of storage.Store needed for symbolication.
type SourceMapStore interface {
	FindSourceMap(ctx context.Context, release, dist, bundleURL string) ([]byte, error)
}

// SymbolicateEvent annotates JS stack frames in evt with original position
// information looked up from stored source maps. Returns the annotated event.
func SymbolicateEvent(ctx context.Context, evt event.Event, store SourceMapStore) event.Event {
	frames := evt.Exception.Stacktrace
	if len(frames) == 0 {
		return evt
	}

	release := eventRelease(evt)
	dist := eventDist(evt)

	for i, frame := range frames {
		if !isMinifiedJS(frame.File) {
			continue
		}
		if frame.Line == 0 && frame.Column == 0 {
			continue
		}

		blob, err := store.FindSourceMap(ctx, release, dist, frame.File)
		if err != nil {
			log.Printf("symbolicate: find source map for %s release=%s dist=%s: %v", frame.File, release, dist, err)
			continue
		}
		if blob == nil {
			continue
		}

		pos, snippet := sourcemap.ResolveWithSnippet(blob, frame.Line, frame.Column)
		if pos == nil {
			continue
		}

		frames[i].OriginalFile = pos.File
		frames[i].OriginalLine = pos.Line
		frames[i].OriginalColumn = pos.Column
		if pos.Function != "" {
			frames[i].OriginalFunction = pos.Function
		}
		if snippet != "" {
			frames[i].Snippet = snippet
		}
	}

	evt.Exception.Stacktrace = frames
	return evt
}

func isMinifiedJS(file string) bool {
	return strings.HasSuffix(file, ".js") || strings.HasSuffix(file, ".mjs")
}

func eventRelease(evt event.Event) string {
	if r, ok := evt.Attributes["release"].(string); ok && r != "" {
		return r
	}
	if r, ok := evt.Resource["release"].(string); ok && r != "" {
		return r
	}
	return ""
}

func eventDist(evt event.Event) string {
	if d, ok := evt.Attributes["dist"].(string); ok && d != "" {
		return d
	}
	if d, ok := evt.Resource["dist"].(string); ok && d != "" {
		return d
	}
	return ""
}
