package integration

import (
	"context"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	clientv3 "go.etcd.io/etcd/client/v3"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRepairAnalysisRegistrationReadiness(t *testing.T) {
	ctx, _, _, _, _ := setup(t)
	endpoints := config.Load().Endpoints()
	name := fmt.Sprintf("repair-health-%d", time.Now().UnixNano())
	srv, err := analysis.StartServer(ctx, endpoints, "127.0.0.1:0", "", "repair", analysis.RuleExtractor{}, 2, 0, name)
	must(t, err)
	defer srv.Close()
	health := func() int {
		r := httptest.NewRecorder()
		srv.HealthHandler().ServeHTTP(r, httptest.NewRequest("GET", "/readyz", nil))
		return r.Code
	}
	if health() != 200 {
		t.Fatal("initial ready")
	}
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	must(t, err)
	defer cli.Close()
	key := "/kanarpc/services/" + name + "/" + srv.Server.Addr().String()
	before, err := cli.Get(ctx, key)
	must(t, err)
	if len(before.Kvs) != 1 {
		t.Fatal("not registered")
	}
	lease := before.Kvs[0].Lease
	_, err = cli.Revoke(ctx, clientv3.LeaseID(lease))
	must(t, err)
	degraded := false
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		if health() == 503 {
			degraded = true
		}
		select {
		case e := <-srv.Done:
			t.Fatal("server exited", e)
		default:
		}
		after, e := cli.Get(ctx, key)
		if e == nil && len(after.Kvs) == 1 && after.Kvs[0].Lease != lease && health() == 200 {
			if !degraded {
				t.Fatal("readiness never reported lease loss")
			}
			client, e := analysis.NewClient(endpoints, 1, time.Second, name)
			must(t, e)
			defer client.Close()
			_, e = client.Call(context.Background(), analysis.Request{Text: "tech: go"})
			must(t, e)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("re-registration did not restore readiness")
}
