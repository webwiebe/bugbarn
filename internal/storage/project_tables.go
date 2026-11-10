package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// mergeMode says what MergeProjects does with a table's rows for the source
// project.
type mergeMode int

const (
	// mergeMove re-points every source row at the target project.
	mergeMove mergeMode = iota
	// mergeMoveNew re-points the source rows whose key the target lacks. A
	// source row that would collide with a target row stays behind and goes
	// with the source project, so the target's own value wins.
	mergeMoveNew
	// mergeDrop leaves the rows with the source project, where they are
	// deleted with it.
	mergeDrop
)

// projectTable is one table that owns rows by project_id.
type projectTable struct {
	name  string
	merge mergeMode
}

// projectTables is the one list of project-owned tables. DeleteProject walks it
// in order (children first, since events and issue_facets reference issues) and
// MergeProjects applies each table's mergeMode. A table with a project_id column
// that is missing here is caught by TestProjectTablesCoverEverySchemaTable.
//
// Moved tables and why:
//   - api_keys: keys already handed to clients keep ingesting, now into the
//     target. api_keys.project_id has no ON DELETE action, so a key left behind
//     would block the final DELETE of the source project.
//   - project_aliases: an alias that pointed at the source follows the data.
//   - log_entries, held_events: no foreign key, so a row left behind would be
//     orphaned instead of deleted.
//   - releases, source_maps, alerts, analytics_pageviews, regression_events,
//     issue_facets, issues, events: history and configuration of the merged data.
//
// Skipped on collision: analytics_daily (primary key per day, page and
// dimension) and monitors (unique slug per project).
// Dropped: settings (the target keeps its own) and key_requests (short-lived
// approval requests for the source project).
var projectTables = []projectTable{
	{"issue_facets", mergeMove},
	{"regression_events", mergeMove},
	{"events", mergeMove},
	{"issues", mergeMove},
	{"analytics_pageviews", mergeMove},
	{"analytics_daily", mergeMoveNew},
	{"releases", mergeMove},
	{"alerts", mergeMove},
	{"settings", mergeDrop},
	{"source_maps", mergeMove},
	{"api_keys", mergeMove},
	{"log_entries", mergeMove},
	{"project_aliases", mergeMove},
	{"held_events", mergeMove},
	{"key_requests", mergeDrop},
	{"monitors", mergeMoveNew},
}

// deleteProjectRows deletes every project-owned row in batches, in registry order.
func deleteProjectRows(ctx context.Context, db *sql.DB, projectID int64) error {
	for _, t := range projectTables {
		if err := deleteInBatches(ctx, db, t.name, projectID); err != nil {
			return apperr.Internal(fmt.Sprintf("delete %s", t.name), err)
		}
	}
	return nil
}

// moveProjectRows re-points the source project's rows at the target inside tx.
func moveProjectRows(ctx context.Context, tx *sql.Tx, sourceID, targetID int64) error {
	for _, t := range projectTables {
		var verb string
		switch t.merge {
		case mergeMove:
			verb = "UPDATE"
		case mergeMoveNew:
			verb = "UPDATE OR IGNORE"
		default:
			continue
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`%s %s SET project_id = ? WHERE project_id = ?`, verb, t.name), targetID, sourceID); err != nil {
			return apperr.Internal("merge: move "+t.name, err)
		}
	}
	return nil
}
