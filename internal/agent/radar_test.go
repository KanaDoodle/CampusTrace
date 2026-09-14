package agent

import (
	"context"
	"strings"
	"testing"
)

func TestRadarToolValidationAndRouting(t *testing.T) {
	for _, v := range []struct{ name, args string }{{"get_daily_digest", `{"user_id":"x"}`}, {"get_watched_sources", `null`}, {"get_closing_jobs", `{"days":0}`}, {"get_closing_jobs", `{"days":7,"other":1}`}, {"get_recent_changes", `{"days":3}`}, {"watch_source", `{"source_id":"x","check_interval":0}`}, {"unwatch_source", `{"watch_id":"bad"}`}} {
		if Validate(v.name, []byte(v.args)) == nil {
			t.Fatal("accepted invalid", v)
		}
	}
	for _, v := range []struct{ q, tool string }{{"今天有什么值得处理？", "get_daily_digest"}, {"未来三天哪些岗位截止？", "get_closing_jobs"}, {"最近哪些岗位关闭了？", "get_recent_changes"}} {
		reply, err := (DemoModel{}).Next(context.Background(), []Message{{Role: "user", Content: v.q}}, nil)
		if err != nil || len(reply.Calls) != 1 || reply.Calls[0].Name != v.tool {
			t.Fatal(v, reply, err)
		}
	}
	for _, def := range (&Tools{}).Definitions() {
		if strings.Contains(def.Name, "watch_source") && !def.Write {
			t.Fatal("watch writer mislabeled")
		}
	}
}
