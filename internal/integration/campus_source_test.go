package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestNationwideCampusCitiesArePreservedOnImport(t *testing.T) {
	ctx, s, _, owner, _ := radarSetup(t)
	reg, err := s.CreateCampusSource(ctx, owner, "jd", "present", "全国校招测试", d.WatchInput{CheckInterval: 3600, Enabled: true})
	must(t, err)
	places := []string{}
	for i := 0; i < 39; i++ {
		places = append(places, fmt.Sprintf("城市%d", i))
	}
	in := persistence.Ingest{SourceID: reg.Source.ID, ExternalID: "39", URL: "https://campus.jd.com/#/details?id=39", Company: "全国校招测试公司", Title: "全国岗位", JobType: "FULL_TIME", Locations: places, Text: "工作地点原文保留。任职要求：熟悉 Go。", FetchStatus: "SUCCESS"}
	_, err = s.Ingest(ctx, in)
	must(t, err)
	page, err := s.SourceJobsForUser(ctx, owner, reg.Source.ID, 1)
	must(t, err)
	if page.Total != 1 || len(page.Jobs[0].Locations) != 39 || page.Jobs[0].Locations[38] != "城市38" {
		t.Fatalf("nationwide cities truncated %+v", page)
	}
	in.Locations = make([]string, d.MaxJobLocations+1)
	if _, err = s.Ingest(ctx, in); err == nil {
		t.Fatal("accepted unbounded location list")
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

func TestGraduateSourceRegistrationIsScopedPrivateAndIdempotent(t *testing.T) {
	ctx, s, q, owner, _ := radarSetup(t)
	other, err := s.NewUser(ctx, d.ID()+"@graduate-source.test", "unused")
	must(t, err)
	for adapter, tenant := range map[string]string{"baidu": "GRADUATE", "meituan": "graduate", "jd": "present", "netease": "103"} {
		input := d.WatchInput{CheckInterval: 3600, Enabled: true, Adaptive: true}
		reg, err := s.CreateCampusSource(ctx, owner, adapter, tenant, "校招来源测试", input)
		must(t, err)
		if reg.Existing || reg.Source.Adapter != adapter || reg.Source.Visibility != "PRIVATE" || reg.Source.OwnerID != owner || reg.Source.Trust != "MANUAL" {
			t.Fatalf("unexpected registration %+v", reg)
		}
		again, err := s.CreateCampusSource(ctx, owner, adapter, tenant, "校招来源测试", input)
		must(t, err)
		if !again.Existing || again.Watch.ID != reg.Watch.ID || again.Source.ID != reg.Source.ID {
			t.Fatalf("duplicate source %+v", again)
		}
		if _, err := s.SourceJobsForUser(ctx, other, reg.Source.ID, 1); err == nil {
			t.Fatal("private source leaked")
		}
		input.SourceID = reg.Source.ID
		input.CheckInterval = 300
		if _, err := s.UpdateWatch(ctx, owner, reg.Watch.ID, input); err == nil {
			t.Fatal("accepted interval below source floor")
		}
		input.CheckInterval = 3600
		input.Direction = "rd"
		if _, err := s.UpdateWatch(ctx, owner, reg.Watch.ID, input); err == nil {
			t.Fatal("accepted unimplemented direction filter")
		}
		if _, err := s.CreateCampusSource(ctx, owner, adapter, "intern", "错误范围", input); err == nil {
			t.Fatal("accepted internship source")
		}
	}
	authn := auth.Service{Store: s, Secret: []byte("graduate-catalog-fixture-secret-32")}
	token, err := authn.Token(owner)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	req := httptest.NewRequest("GET", "/api/sources/catalog", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte("baidu")) || !bytes.Contains(response.Body.Bytes(), []byte("meituan")) || !bytes.Contains(response.Body.Bytes(), []byte("jd")) || !bytes.Contains(response.Body.Bytes(), []byte("netease")) {
		t.Fatalf("catalog %d %s", response.Code, response.Body.String())
	}
}
