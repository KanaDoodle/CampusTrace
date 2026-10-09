package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
)

type CampusRegistration struct {
	Source   d.Source      `json:"source"`
	Watch    d.WatchTarget `json:"watch"`
	Existing bool          `json:"existing"`
}

// CreateCampusWatch keeps a user-entered recruitment site private and creates
// its schedule in the same transaction. The caller has already resolved the
// supported public URL to a project code through the source adapter.
func (s *Store) CreateCampusWatch(ctx context.Context, user, projectCode, projectName string, input d.WatchInput) (CampusRegistration, error) {
	if projectName == "" {
		return CampusRegistration{}, ErrValidation
	}
	return s.CreateCampusSource(ctx, user, "xiaohongshu", projectCode, "小红书 · "+projectName, input)
}

func (s *Store) CreateCampusSource(ctx context.Context, user, adapter, projectCode, name string, input d.WatchInput) (CampusRegistration, error) {
	var out CampusRegistration
	if err := validateCampusSource(user, adapter, projectCode, name); err != nil {
		return out, err
	}
	checked := input
	checked.SourceID = "pending"
	if checked.Validate() != nil || input.CheckInterval < d.SourceMinimumInterval(adapter) || (adapter != "xiaohongshu" && input.Direction != "") {
		return out, ErrValidation
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
			return err
		}
		src, err := campusSourceTx(ctx, tx, user, adapter, projectCode, name)
		if err != nil {
			return err
		}
		out.Source = src
		watch, err := One[d.WatchTarget](ctx, tx, "SELECT body FROM watch_targets WHERE user_id=? AND source_id=? AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.one_shot')),'false')='false' ORDER BY id LIMIT 1", user, src.ID)
		if err == nil {
			out.Watch = watch
			out.Existing = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		input.SourceID = src.ID
		out.Watch, err = createWatchTx(ctx, tx, user, input)
		return err
	})
	return out, err
}

type WatchProgress struct {
	Expected  int `json:"expected"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

func (s *Store) ProgressForUser(ctx context.Context, user, id string) (WatchProgress, error) {
	var out WatchProgress
	watch, err := s.Watch(ctx, user, id)
	if err != nil {
		return out, err
	}
	err = s.DB.QueryRowContext(ctx, "SELECT expected_count,completed_count,failed_count FROM watch_runs WHERE watch_id=? AND schedule_version=?", id, watch.ScheduleVersion).Scan(&out.Expected, &out.Completed, &out.Failed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	return out, err
}

type SourceJobsPage struct {
	Jobs     []d.Job `json:"jobs"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

func (s *Store) SourceJobsForUser(ctx context.Context, user, sourceID string, page int) (SourceJobsPage, error) {
	out := SourceJobsPage{Jobs: []d.Job{}, Page: page, PageSize: 50}
	if page < 1 || page > 10 {
		return out, ErrValidation
	}
	if _, err := watchSource(ctx, s.DB, user, sourceID); err != nil {
		return out, err
	}
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(DISTINCT p.job_id) FROM postings p JOIN jobs j ON j.id=p.job_id WHERE p.source_id=? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))", sourceID, user).Scan(&out.Total)
	if err != nil {
		return out, err
	}
	out.Jobs, err = Many[d.Job](ctx, s.DB, "SELECT j.body FROM postings p JOIN jobs j ON j.id=p.job_id WHERE p.source_id=? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?)) GROUP BY j.id ORDER BY j.id LIMIT 50 OFFSET ?", sourceID, user, (page-1)*50)
	return out, err
}

func validateCampusSource(user, adapter, projectCode, name string) error {
	validScope := (adapter == "kedacom" && projectCode == "campus_2") || (adapter == "games37" && projectCode == "campus") || (adapter == "sangfor" && projectCode == "2027_regular_101") || (adapter == "yonyou" && projectCode == "106301") || (adapter == "bankcomm_tech" && projectCode == "campus_head_it") || (adapter == "cms_securities" && projectCode == "101501") || (adapter == "htsc_securities" && projectCode == "107301") || ((adapter == "csc_securities" || adapter == "guosen_securities" || adapter == "galaxy_securities" || adapter == "cicc_securities") && projectCode == "campus") || (adapter == "cmb_tech" && projectCode == "graduate_tech") || (adapter == "citic_tech" && projectCode == "campus_it") || (adapter == "boc_software" && projectCode == "2027_software") || (adapter == "boc_operations" && projectCode == "2027_operations") || (adapter == "sap" && projectCode == "CN_Graduate") || (adapter == "tencent" && projectCode == "2027_cn_1") || (adapter == "tcl_digital" && projectCode == "308501_101206") || (adapter == "tcl_honghu" && projectCode == "308501_364906") || (adapter == "cec_software" && projectCode == "graduate_software") || (adapter == "gbits" && projectCode == "8a82ac07a057a3ea01a0617904b4100c") || (adapter == "mihoyo" && projectCode == "13") || (adapter == "pingan_tech" && projectCode == "graduate_PA011") || (adapter == "pingan_oneconnect" && projectCode == "graduate_PA038") || (adapter == "pingan_wallet" && projectCode == "graduate_PA027") || (adapter == "cmcloud" && projectCode == "79") || (adapter == "cmiot" && projectCode == "77") || (adapter == "cmhome" && projectCode == "81") || (adapter == "ths" && projectCode == "61") || (adapter == "cmbnt" && projectCode == "graduate") || (adapter == "netease_game" && projectCode == "102") || (adapter == "leihuo" && projectCode == "77") || (adapter == "ctyun" && projectCode == "101101_581854") || (adapter == "ctcloud" && projectCode == "101101_581851") || (adapter == "honor" && projectCode == "101801") || (adapter == "ctrip" && projectCode == "campus") || ((adapter == "h3c" || adapter == "yusys" || adapter == "cksic" || adapter == "whxmc" || adapter == "neusoft" || adapter == "mthreads" || adapter == "nexchip" || adapter == "qihoo360" || adapter == "sany" || adapter == "inovance" || adapter == "vivo" || adapter == "sgm" || adapter == "hundsun" || adapter == "yuewen") && projectCode == "campus") || (adapter == "lenovo" && projectCode == "1") || (adapter == "midea" && projectCode == "055bb05d-1957-4ea0-bb21-873ca0164d84") || (adapter == "byd" && projectCode == "2076475538687475714") || (adapter == "hikvision" && projectCode == "e198653730e14820b9e95b29fbc2223f") || (adapter == "siemens" && projectCode == "CAMPUSRECRUITMENT") || (adapter == "haier" && projectCode == "68") || (adapter == "oppo" && projectCode == "30") || adapter == "xiaohongshu" || (adapter == "baidu" && projectCode == "GRADUATE") || (adapter == "meituan" && projectCode == "graduate") || (adapter == "jd" && projectCode == "present") || (adapter == "netease" && projectCode == "103") || (adapter == "alibaba" && projectCode == "100000760001") || (adapter == "bilibili" && projectCode == "freshmen") || (adapter == "kuaishou" && projectCode == "20271779425607")
	if user == "" || projectCode == "" || len(projectCode) > 100 || len(name) == 0 || len(name) > 160 || strings.ContainsAny(projectCode, "/:?@#") || !validScope {
		return ErrValidation
	}
	return nil
}

// The caller holds the user lock, serializing private source identity creation.
func campusSourceTx(ctx context.Context, tx *sql.Tx, user, adapter, projectCode, name string) (d.Source, error) {
	src, err := One[d.Source](ctx, tx, "SELECT body FROM sources WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.owner_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.adapter'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.tenant'))=? LIMIT 1", user, adapter, projectCode)
	if errors.Is(err, sql.ErrNoRows) {
		src = d.Source{ID: d.ID(), Name: name, Adapter: adapter, Tenant: projectCode, RateLimit: 30, OwnerID: user, Visibility: "PRIVATE", Type: "MANUAL", Trust: "MANUAL", Timezone: "Asia/Shanghai"}
		if _, err = tx.ExecContext(ctx, "INSERT INTO sources(id,body) VALUES(?,?)", src.ID, d.JSON(src)); err != nil {
			return src, err
		}
	} else if err != nil {
		return src, err
	}
	return src, nil
}
