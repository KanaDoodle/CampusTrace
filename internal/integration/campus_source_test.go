package integration

import (
	"bytes"
	"encoding/json"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"github.com/redis/go-redis/v9"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCampusSourceRegistrationAndOwnership(t *testing.T) {
	ctx, store, queue, owner, _ := radarSetup(t)
	second, err := store.NewUser(ctx, d.ID()+"@campus-source.test", "unused")
	must(t, err)
	input := d.WatchInput{CheckInterval: 3600, Direction: "rd", Enabled: true}
	registration, err := store.CreateCampusWatch(ctx, owner, "campus_autumn_27", "2027 校园招聘", input)
	must(t, err)
	if registration.Existing || registration.Source.OwnerID != owner || registration.Source.Visibility != "PRIVATE" || registration.Watch.Direction != "rd" {
		t.Fatalf("invalid private registration: %+v", registration)
	}
	tooFrequent := input
	tooFrequent.SourceID = registration.Source.ID
	tooFrequent.CheckInterval = 300
	if _, err := store.UpdateWatch(ctx, owner, registration.Watch.ID, tooFrequent); err == nil {
		t.Fatal("accepted scan interval shorter than source pacing")
	}
	again, err := store.CreateCampusWatch(ctx, owner, "campus_autumn_27", "2027 校园招聘", input)
	must(t, err)
	if !again.Existing || again.Source.ID != registration.Source.ID || again.Watch.ID != registration.Watch.ID {
		t.Fatalf("duplicate registration: %+v", again)
	}
	if _, err := store.SourceJobsForUser(ctx, second, registration.Source.ID, 1); err == nil {
		t.Fatal("other user read private source jobs")
	}
	if _, err := store.ProgressForUser(ctx, second, registration.Watch.ID); err == nil {
		t.Fatal("other user read private watch progress")
	}
	page, err := store.SourceJobsForUser(ctx, owner, registration.Source.ID, 1)
	must(t, err)
	if page.Total != 0 || page.PageSize != 50 {
		t.Fatalf("empty source page: %+v", page)
	}
	authn := auth.Service{Store: store, Secret: []byte("synthetic-campus-source-test-secret-32")}
	token, err := authn.Token(owner)
	must(t, err)
	api := (&transport.API{Store: store, Queue: queue, Auth: authn, Metrics: observability.New()}).Handler()
	request := httptest.NewRequest("POST", "/api/sources/preview", bytes.NewBufferString(`{"url":"https://example.com/jobs"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 400 || !bytes.Contains(response.Body.Bytes(), []byte("SOURCE_URL_UNSUPPORTED")) {
		t.Fatalf("unsupported URL: %d %s", response.Code, response.Body.String())
	}
}

func TestCampusSourceLocalPaceDoesNotSpendRetry(t *testing.T) {
	ctx, store, queue, owner, sourceID := radarSetup(t)
	_, task := scheduledWatch(t, ctx, store, owner, sourceID)
	must(t, queue.Init(ctx))
	must(t, queue.Publish(ctx, task))
	messages, err := queue.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: queue.Group(), Consumer: "source-pace", Streams: []string{queue.Stream(), ">"}, Count: 1}).Result()
	must(t, err)
	must(t, queue.DeferRateLimited(ctx, messages[0].Messages[0].ID, task))
	entries, err := queue.R.ZRangeWithScores(ctx, queue.Prefix+"retry:WATCH_CHECK", 0, -1).Result()
	must(t, err)
	if len(entries) != 1 || entries[0].Score < float64(time.Now().Add(50*time.Second).UnixMilli()) {
		t.Fatalf("pace delay: %+v", entries)
	}
	var deferred persistence.Task
	must(t, json.Unmarshal([]byte(entries[0].Member.(string)), &deferred))
	if deferred.Attempt != task.Attempt || deferred.ID != task.ID {
		t.Fatalf("pace changed retry identity: %+v", deferred)
	}
}
