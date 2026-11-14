package spool

import "fmt"

// Pending is an unprocessed record with the cursor position that marks it
// handled.
type Pending struct {
	Record Record
	// After is the position to commit once the record is handled.
	After Position
	// Drained names the rotated segment this record finishes, or "". Delete it
	// after After is committed durably.
	Drained string
}

// ReadPending returns every record the worker has not handled, in order: the
// rest of the segment the cursor names, then the active segment. It is the same
// span ReadBacklog measures, with the same reading of a cursor that names a
// segment that was never rotated (resume the active segment at that offset).
// It does not write.
func ReadPending(dir string) ([]Pending, error) {
	pos, err := ReadPosition(dir)
	if err != nil {
		return nil, fmt.Errorf("spool pending: read cursor: %w", err)
	}
	if pos.Segment != "" {
		exists, err := SegmentExists(dir, pos.Segment)
		if err != nil {
			return nil, err
		}
		if !exists {
			pos = Position{Offset: pos.Offset}
		}
	}

	var out []Pending
	if pos.Segment != "" {
		entries, err := ReadRecordsFrom(SegmentPath(dir, pos.Segment), pos.Offset)
		if err != nil {
			return nil, err
		}
		for i, e := range entries {
			p := Pending{Record: e.Record, After: Position{Segment: pos.Segment, Offset: e.EndOffset}}
			if i == len(entries)-1 {
				p.After, p.Drained = Position{}, pos.Segment
			}
			out = append(out, p)
		}
		pos = Position{}
	}

	entries, err := ReadRecordsFrom(Path(dir), pos.Offset)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		out = append(out, Pending{Record: e.Record, After: Position{Offset: e.EndOffset}})
	}
	return out, nil
}
