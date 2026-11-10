package storage

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

type stringerValue string

func (s stringerValue) String() string { return string(s) }

// flattenFacets turns a nested payload into dotted-key rows. Blank strings,
// blank Stringers, nil values and blank keys are skipped; other scalars are
// rendered as text.
func TestFlattenFacetsScalars(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"s":      "text",
		"blank":  "  ",
		"nil":    nil,
		"b":      true,
		"f":      1.5,
		"i":      7,
		"i64":    int64(9),
		"nested": map[string]any{"k": "v"},
		"list":   []any{"a", 2.0},
		"when":   stringerValue("stringer"),
		"empty":  stringerValue(" "),
		"dur":    90 * time.Second,
		"other":  uint8(3),
	}
	var got []facetRow
	flattenFacets(&got, "", payload)

	want := []facetRow{
		{key: "b", value: "true"},
		{key: "dur", value: "1m30s"},
		{key: "f", value: "1.5"},
		{key: "i", value: "7"},
		{key: "i64", value: "9"},
		{key: "list[0]", value: "a"},
		{key: "list[1]", value: "2"},
		{key: "nested.k", value: "v"},
		{key: "other", value: "3"},
		{key: "s", value: "text"},
		{key: "when", value: "stringer"},
	}
	sort.Slice(got, func(i, j int) bool { return got[i].key < got[j].key })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}

	var bare []facetRow
	flattenFacets(&bare, "", "top level scalar has no key")
	flattenFacets(&bare, "  ", 5)
	if len(bare) != 0 {
		t.Fatalf("a blank key must produce no row, got %+v", bare)
	}
}
