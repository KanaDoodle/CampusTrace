package integration

import (
	"errors"
	"github.com/go-sql-driver/mysql"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestFixtureScopeRemovesUntrackedGlobalCatalog(t *testing.T) {
	ctx, parent, _, _, source := setup(t)
	kept, err := parent.Ingest(ctx, ingest(t, ctx, parent, source))
	must(t, err)
	var parentSchema string
	must(t, parent.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&parentSchema))
	var job, childSchema string
	t.Run("additional-source", func(t *testing.T) {
		ctx, store, _, _, _ := setup(t)
		must(t, store.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&childSchema))
		var count int
		err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+parentSchema+"`.jobs").Scan(&count)
		var denied *mysql.MySQLError
		if !errors.As(err, &denied) || denied.Number != 1142 && denied.Number != 1044 {
			t.Fatal("a test account could read another fixture database", err)
		}
		// Several catalog tests create additional public sources. They have no
		// owner and are not the primary source returned by setup.
		source := d.ID()
		must(t, store.SaveSource(ctx, d.Source{ID: source, Name: "untracked global fixture", Trust: "OFFICIAL"}))
		observation, err := store.Ingest(ctx, ingest(t, ctx, store, source))
		must(t, err)
		job = observation.JobID
	})
	var count int
	must(t, parent.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE id=?", job).Scan(&count))
	if count != 0 {
		t.Fatal("a finished test scope left an unrelated public job visible to later fixtures")
	}
	_, err = parent.Job(ctx, kept.JobID)
	must(t, err)
	if parentSchema == childSchema {
		t.Fatal("independent test scopes reused a database")
	}
}
