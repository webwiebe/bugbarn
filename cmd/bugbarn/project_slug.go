package main

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// projectSlugResolver maps a project ID to its slug for the ingest handler's
// foreign-project-header measurement (#235).
func projectSlugResolver(store *storage.Store) func(ctx context.Context, projectID int64) (string, error) {
	return func(ctx context.Context, projectID int64) (string, error) {
		proj, err := store.ProjectByID(ctx, projectID)
		if err != nil {
			return "", err
		}
		return proj.Slug, nil
	}
}
