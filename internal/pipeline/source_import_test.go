package pipeline

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"testing"
)

func TestSourceImportEnvelopeAndFailureCategories(t *testing.T) {
	tsk := p.NewTask("SOURCE_IMPORT", d.ID())
	tsk.Generation = 1
	tsk.ID = p.SourceImportTaskID(tsk.EntityID, tsk.Generation)
	if err := ValidateTask(tsk); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*p.Task){func(t *p.Task) { t.Generation = 0 }, func(t *p.Task) { t.Generation++ }, func(t *p.Task) { t.WatchID = t.EntityID }, func(t *p.Task) { t.ScheduleVersion = 1 }, func(t *p.Task) { t.Posting = &p.Ingest{} }, func(t *p.Task) { t.ProcessingVersion = "secret" }, func(t *p.Task) { t.EntityID = "short" }, func(t *p.Task) { t.CorrelationID = "" }} {
		bad := tsk
		mutate(&bad)
		if ValidateTask(bad) == nil {
			t.Fatalf("accepted invalid %+v", bad)
		}
	}
	for _, c := range []struct {
		err  error
		code string
	}{{context.DeadlineExceeded, "SOURCE_IMPORT_NETWORK"}, {&source.FetchError{Category: "BLOCKED"}, "SOURCE_IMPORT_BLOCKED"}, {&source.FetchError{Category: "CAPACITY"}, "SOURCE_IMPORT_CAPACITY"}, {&source.FetchError{Category: "SCHEMA_INVALID"}, "SOURCE_IMPORT_CHANGED"}, {&source.FetchError{Category: "RATE_LIMIT"}, "SOURCE_IMPORT_BUSY"}} {
		if got := sourceImportFailure(c.err); got != c.code {
			t.Fatal(got, c.code)
		}
	}
}
