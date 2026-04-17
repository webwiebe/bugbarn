package sourcemap

import (
	"strings"

	gosourcemap "github.com/go-sourcemap/sourcemap"
)

// OriginalPosition holds the resolved original position from a source map.
type OriginalPosition struct {
	File     string
	Function string
	Line     int
	Column   int
}

// Resolve looks up the original position for a generated line+column in mapData.
// mapData is raw source map JSON. Returns nil if not found or on error.
func Resolve(mapData []byte, generatedLine, generatedColumn int) *OriginalPosition {
	consumer, err := gosourcemap.Parse("", mapData)
	if err != nil {
		return nil
	}

	source, name, line, column, ok := consumer.Source(generatedLine, generatedColumn)
	if !ok || source == "" {
		return nil
	}

	return &OriginalPosition{
		File:     source,
		Function: name,
		Line:     line,
		Column:   column,
	}
}

// SnippetFromSource extracts a short 3-line context snippet around sourceLine
// from sourceContent (the original source file content, if embedded in the map).
// sourceLine is 1-based.
func SnippetFromSource(sourceContent string, sourceLine int) string {
	if sourceContent == "" || sourceLine <= 0 {
		return ""
	}

	lines := strings.Split(sourceContent, "\n")
	total := len(lines)

	// Convert 1-based sourceLine to 0-based index.
	idx := sourceLine - 1
	if idx < 0 || idx >= total {
		return ""
	}

	start := idx - 1
	if start < 0 {
		start = 0
	}
	end := idx + 1
	if end >= total {
		end = total - 1
	}

	return strings.Join(lines[start:end+1], "\n")
}

// ResolveWithSnippet resolves the original position and, if the source map
// embeds source contents, also returns a code snippet around the original line.
func ResolveWithSnippet(mapData []byte, generatedLine, generatedColumn int) (*OriginalPosition, string) {
	consumer, err := gosourcemap.Parse("", mapData)
	if err != nil {
		return nil, ""
	}

	source, name, line, column, ok := consumer.Source(generatedLine, generatedColumn)
	if !ok || source == "" {
		return nil, ""
	}

	pos := &OriginalPosition{
		File:     source,
		Function: name,
		Line:     line,
		Column:   column,
	}

	snippet := SnippetFromSource(consumer.SourceContent(source), line)
	return pos, snippet
}
