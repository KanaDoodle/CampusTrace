package source

import (
	"context"
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestXiaohongshuPreviewDiscoveryAndDetail(t *testing.T) {
	listCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/findEnumList") {
			json.NewEncoder(w).Encode(map[string]any{"statusCode": 200, "success": true, "data": map[string]any{"PositionProjectEnum": []any{map[string]any{"code": "campus_autumn_27", "name": "2027 校园招聘", "extMap": map[string]string{"category": "regular"}}}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pageQueryPosition") {
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("incorrect request %s %s", r.Method, r.Header.Get("Content-Type"))
			}
			var input struct {
				PageNum     int      `json:"pageNum"`
				PageSize    int      `json:"pageSize"`
				JobProjects []string `json:"jobProjects"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.PageSize != 100 || len(input.JobProjects) != 1 || input.JobProjects[0] != "campus_autumn_27" {
				t.Errorf("incorrect project scope: %+v", input)
			}
			listCalls++
			start, end := 1, 100
			if input.PageNum == 2 {
				start, end = 101, 101
			}
			rows := []any{}
			for id := start; id <= end; id++ {
				direction := "rd"
				if id == 101 {
					direction = "algorithm"
				}
				rows = append(rows, map[string]any{"positionId": id, "positionName": "后端开发 " + strconv.Itoa(id), "jobProjectName": "2027 校园招聘", "workplace": "上海、北京", "direction": direction})
			}
			json.NewEncoder(w).Encode(map[string]any{"statusCode": 200, "success": true, "data": map[string]any{"pageNum": input.PageNum, "total": 101, "totalPage": 2, "list": rows}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/queryPositionDetail") {
			json.NewEncoder(w).Encode(map[string]any{"statusCode": 200, "success": true, "data": map[string]any{"positionId": 101, "positionName": "后端开发 101", "jobProject": "campus_autumn_27", "jobProjectName": "2027 校园招聘", "workplace": "上海", "duty": "构建服务", "qualification": "熟悉 Go", "recruitStatus": "in_recruitment", "applyLimit": 5}})
			return
		}
		t.Errorf("unexpected path: %s", r.URL.Path)
	}))
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	preview, err := a.PreviewXHS(context.Background(), XHSURL)
	if err != nil || preview.Total != 101 || preview.ProjectCode != "campus_autumn_27" || len(preview.Samples) != 5 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	src := d.Source{ID: "fixture", Adapter: "xiaohongshu", Tenant: preview.ProjectCode}
	refs, err := a.Discover(context.Background(), src, d.WatchTarget{WatchInput: d.WatchInput{Direction: "algorithm"}})
	if err != nil || len(refs) != 1 || refs[0].ExternalID != "101" || refs[0].URL != XHSURL+"/101" {
		t.Fatalf("discover: %+v %v", refs, err)
	}
	if listCalls != 3 {
		t.Fatalf("expected preview and both pages, got %d", listCalls)
	}
	result, err := a.FetchPosting(context.Background(), src, refs[0])
	if err != nil || !strings.Contains(result.Text, "熟悉 Go") || !strings.Contains(result.Text, "最多可投职位数：5") {
		t.Fatalf("detail: %+v %v", result, err)
	}
}

func TestXiaohongshuURLAndIncompleteScan(t *testing.T) {
	for _, raw := range []string{"http://job.xiaohongshu.com/campus/position", "https://other.example/campus/position", "https://job.xiaohongshu.com/campus/position?next=evil", "https://job.xiaohongshu.com/campus/position/1"} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("accepted unsupported URL", raw)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"statusCode": 200, "success": true, "data": map[string]any{"pageNum": 1, "total": 2, "totalPage": 1, "list": []any{map[string]any{"positionId": 1, "positionName": "职位", "jobProjectName": "校园招聘"}}}})
	}))
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	_, err := a.Discover(context.Background(), d.Source{Adapter: "xiaohongshu", Tenant: "campus_autumn_27"}, d.WatchTarget{})
	var fetch *FetchError
	if !AsFetchError(err, &fetch) || fetch.Category != "SCHEMA_INVALID" {
		t.Fatalf("incomplete scan accepted: %v", err)
	}
}

func TestXiaohongshuLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("set CAMPUS_LIVE_SOURCES=1 for public website verification")
	}
	a := PublicPlatform{}
	preview, err := a.PreviewXHS(context.Background(), XHSURL)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Total < 1 || preview.Total > MaxPostings || preview.ProjectCode == "" {
		t.Fatalf("unexpected preview %+v", preview)
	}
	src := d.Source{ID: "live", Adapter: "xiaohongshu", Tenant: preview.ProjectCode}
	refs, err := a.Discover(context.Background(), src, d.WatchTarget{})
	if err != nil || len(refs) != preview.Total {
		t.Fatalf("discovery count %d of %d: %v", len(refs), preview.Total, err)
	}
	result, err := a.FetchPosting(context.Background(), src, refs[0])
	if err != nil || !strings.Contains(result.Text, "任职资格") {
		t.Fatalf("detail %+v: %v", result, err)
	}
}
