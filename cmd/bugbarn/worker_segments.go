package main

import (
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// Rotated spool segments. The worker rotates the active segment once it passes
// the threshold, drains the rotated segment, then deletes it. See the segment
// lifecycle notes in internal/spool/segments.go for the crash-safety argument.

// restorePosition loads the persisted cursor at startup and deletes rotated
// segments the worker no longer needs.
func (w *spoolWorker) restorePosition() {
	pos, err := spool.ReadPosition(w.spoolDir)
	if err != nil {
		// Without a readable cursor there is no telling which rotated segment is
		// still being drained, so leave every segment on disk.
		slog.Error("worker failed to read cursor, starting from 0", "error", err)
		w.pos = spool.Position{}
		return
	}
	if pos.Segment != "" {
		exists, err := spool.SegmentExists(w.spoolDir, pos.Segment)
		if err != nil {
			slog.Error("worker failed to check spool segment, keeping all segments", "segment", pos.Segment, "error", err)
			w.pos = pos
			return
		}
		if !exists {
			// The cursor is committed before the rename, and a drained segment
			// is deleted only after the cursor has moved off it, so a missing
			// segment means the process stopped between the commit and the
			// rename: the records are still in the active segment.
			slog.Info("worker cursor names a spool segment that was never rotated, resuming the active segment",
				"segment", pos.Segment, "offset", pos.Offset)
			pos = spool.Position{Offset: pos.Offset}
		}
	}
	w.pos = pos
	w.removeDrainedSegments()
}

// finishSegment moves the cursor from a fully processed rotated segment to the
// start of the active segment, then deletes the rotated segment. The cursor is
// committed durably first: if the delete never happens, the next start finds a
// segment the cursor does not name and deletes it then.
func (w *spoolWorker) finishSegment() {
	next := spool.Position{}
	if err := spool.CommitPosition(w.spoolDir, next); err != nil {
		// Stay on the segment; the next tick finds it drained and retries.
		slog.Error("worker failed to commit cursor after draining spool segment", "segment", w.pos.Segment, "error", err)
		return
	}
	w.pos = next
	w.removeDrainedSegments()
}

// removeDrainedSegments deletes every rotated segment except the one the cursor
// is reading. Segments the cursor does not name are fully processed: either
// drained by this worker, or left behind by a version that rotated without
// tracking the segment and never read it again.
func (w *spoolWorker) removeDrainedSegments() {
	names, err := spool.ListSegments(w.spoolDir)
	if err != nil {
		slog.Error("worker failed to list spool segments", "error", err)
		return
	}
	for _, name := range names {
		if name == w.pos.Segment {
			continue
		}
		size, err := spool.RemoveSegment(w.spoolDir, name)
		if err != nil {
			slog.Error("worker failed to delete processed spool segment", "segment", name, "error", err)
			continue
		}
		slog.Info("worker deleted processed spool segment", "segment", name, "bytes", size)
	}
}

// rotate rotates the active segment once it exceeds the threshold and switches
// the cursor to the rotated segment, at the same offset, so the records not yet
// processed in it are drained before the new active segment.
func (w *spoolWorker) rotate() {
	if w.eventSpool == nil {
		return
	}
	threshold := w.rotateThreshold
	if threshold <= 0 {
		threshold = workerRotateThreshold
	}
	segment, err := w.eventSpool.RotateIfExceedsAt(threshold, w.pos.Offset)
	if err != nil {
		slog.Error("worker failed to rotate spool", "error", err)
		return
	}
	if segment == "" {
		return
	}
	w.pos = spool.Position{Segment: segment, Offset: w.pos.Offset}
	slog.Info("worker rotated spool segment", "segment", segment, "offset", w.pos.Offset)
}

// pendingRecords counts the records not yet processed: the rest of the segment
// being read plus, while a rotated segment drains, the whole active segment.
func (w *spoolWorker) pendingRecords() int64 {
	remaining, _ := spool.ReadRecordsFrom(spool.SegmentPath(w.spoolDir, w.pos.Segment), w.pos.Offset)
	pending := int64(len(remaining))
	if w.pos.Segment != "" {
		active, _ := spool.ReadRecordsFrom(spool.Path(w.spoolDir), 0)
		pending += int64(len(active))
	}
	return pending
}
