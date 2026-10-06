package pipeline

import (
	"context"
	"errors"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
)

type campusImportResolver interface {
	ResolveCampusImport(context.Context, string) (source.CampusPreview, error)
}

func (w *Worker) processSourceImport(ctx context.Context, t p.Task) error {
	item, err := w.Store.PrepareSourceImport(ctx, t)
	if errors.Is(err, p.ErrStaleSourceImport) {
		return nil
	}
	if err != nil {
		return err
	}
	// A stored envelope cannot extend fetching to arbitrary sites or change scope.
	supported := false
	for _, s := range source.CampusSites() {
		if s.Adapter == item.Adapter && s.URL == item.URL {
			supported = true
			break
		}
	}
	if !supported {
		return p.ErrValidation
	}
	resolver, ok := w.sourceAdapter().(campusImportResolver)
	if !ok {
		return p.ErrValidation
	}
	v, err := resolver.ResolveCampusImport(ctx, item.URL)
	if err != nil {
		return err
	}
	return w.Store.QueueSourceImport(ctx, t, v.Adapter, v.ProjectCode, v.Name)
}
func sourceImportFailure(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "SOURCE_IMPORT_NETWORK"
	}
	var v *source.FetchError
	if source.AsFetchError(err, &v) {
		switch v.Category {
		case "TIMEOUT_OR_NETWORK":
			return "SOURCE_IMPORT_NETWORK"
		case "BLOCKED":
			return "SOURCE_IMPORT_BLOCKED"
		case "RATE_LIMIT", "HTTP_TRANSIENT":
			return "SOURCE_IMPORT_BUSY"
		case "CAPACITY":
			return "SOURCE_IMPORT_CAPACITY"
		case "SCHEMA_INVALID", "SCHEMA_DUPLICATE_ID", "RESPONSE_TOO_LARGE":
			return "SOURCE_IMPORT_CHANGED"
		}
	}
	return "SOURCE_IMPORT_FAILED"
}
