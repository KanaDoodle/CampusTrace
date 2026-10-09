package source

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const BankcommCampusURL = "https://job.bankcomm.com/#/school"
const bankcommOrigin = "https://job.bankcomm.com"
const bankcommHead int64 = 1000000003
const bankcommITProject = "2026年秋季校招（金科类）"

// Public site gateway envelope, with no access or refresh token.
func bankcommInput(params any) url.Values {
	body := map[string]any{"params": params, "unnessaryLogin": false}
	if params == nil {
		body = nil
	}
	message, _ := json.Marshal(map[string]any{"REQ_HEAD": map[string]any{"TRAN_PROCESS": "", "TRAN_ID": "", "ACCESS_TOKEN": nil, "REFRESH_TOKEN": nil}, "REQ_BODY": body})
	return url.Values{"REQ_MESSAGE": {string(message)}}
}

type bankcommEnvelope[T any] struct {
	Head struct {
		Success string `json:"TRAN_SUCCESS"`
	} `json:"RSP_HEAD"`
	Body struct {
		Results T `json:"results"`
	} `json:"RSP_BODY"`
}
type bankcommJob struct {
	ID           int64  `json:"positionId"`
	Title        string `json:"pubName"`
	Kind         string `json:"engageType"`
	UnitID       int64  `json:"bankNumber"`
	Unit         string `json:"bankName"`
	Department   string `json:"deptName"`
	DepartmentID int64  `json:"deptNumber"`
	Project      string `json:"projectName"`
	City         string `json:"workPlace"`
	Deadline     string `json:"endDate"`
	Duties       string `json:"responsibility"`
	Requirements string `json:"require"`
}

func bankcommJobURL(id string) string {
	return bankcommOrigin + "/#/school/recruitmentInfo/?positionId=" + id
}
func bankcommRef(row bankcommJob) (PostingRef, error) {
	if row.ID <= 0 || row.Kind != "1" || row.UnitID != bankcommHead || row.Unit != "总行" || row.Department == "" || row.DepartmentID <= 0 || row.Project == "" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	ref := PostingRef{ExternalID: strconv.FormatInt(row.ID, 10), URL: bankcommJobURL(strconv.FormatInt(row.ID, 10)), Title: row.Title, Company: "交通银行 · 总行 · " + row.Department, JobType: "UNKNOWN", Locations: splitPlaces(row.City)}
	return ref, validateRef(ref)
}
func (a PublicPlatform) bankcommPost(ctx context.Context, s d.Source, name string, params, dst any) error {
	return a.post(ctx, s, bankcommOrigin+"/api/GTMS.GTMS-PORTAL.V-1.0/"+name+".do", bankcommInput(params), dst)
}
func (a PublicPlatform) discoverBankcomm(ctx context.Context, s d.Source) ([]PostingRef, error) {
	var orgs bankcommEnvelope[[]struct {
		Rows []struct {
			ID   string `json:"code"`
			Name string `json:"name"`
		} `json:"list"`
	}]
	if err := a.bankcommPost(ctx, s, "queryOrgNameList", nil, &orgs); err != nil {
		return nil, err
	}
	if orgs.Head.Success != "1" || orgs.Body.Results == nil {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	headFound := 0
	for _, group := range orgs.Body.Results {
		for _, row := range group.Rows {
			if row.ID == strconv.FormatInt(bankcommHead, 10) {
				if row.Name != "总行" {
					return nil, fail("SCHEMA_INVALID", false, 200)
				}
				headFound++
			}
		}
	}
	if headFound != 1 {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return a.bankcommRows(ctx, s)
}
func (a PublicPlatform) bankcommRows(ctx context.Context, s d.Source) ([]PostingRef, error) {
	selected := map[string]bool{}
	all, err := campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v bankcommEnvelope[struct {
			Total *int          `json:"total"`
			Rows  []bankcommJob `json:"policyList"`
		}]
		params := map[string]any{"businessPara": map[string]any{"workPlace": "", "pubName": "", "bankNumber": strconv.FormatInt(bankcommHead, 10), "positionId": "", "engageType": 1}, "pagePara": map[string]any{"pageNum": page, "pageSize": 50}}
		if err := a.bankcommPost(ctx, s, "querySocietyRecruitInfo", params, &v); err != nil {
			return campusPage{}, err
		}
		data := v.Body.Results
		if v.Head.Success != "1" || data.Total == nil || data.Rows == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *data.Total, Page: page, Size: 50}
		for _, row := range data.Rows {
			ref, err := bankcommRef(row)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
			selected[ref.ExternalID] = row.Project == bankcommITProject
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	refs := []PostingRef{}
	for _, row := range all {
		if selected[row.ExternalID] {
			refs = append(refs, row)
		}
	}
	return refs, nil
}
func (a PublicPlatform) fetchBankcomm(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != bankcommJobURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	// Detail does not echo ID or recruiting project. Bind it to the current,
	// completely verified list before accepting the unit/title/detail response.
	refs, err := a.discoverBankcomm(ctx, s)
	if err != nil {
		return "", err
	}
	found := false
	for _, ref := range refs {
		if ref.ExternalID == r.ExternalID && ref.Title == r.Title && ref.Company == r.Company {
			found = true
		}
	}
	if !found {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	var v bankcommEnvelope[bankcommJob]
	if err := a.bankcommPost(ctx, s, "queryPositionDetail", map[string]any{"positionId": r.ExternalID}, &v); err != nil {
		return "", err
	}
	row := v.Body.Results
	if v.Head.Success != "1" || row.Title != r.Title || row.UnitID != bankcommHead || row.Unit != "总行" || row.DepartmentID <= 0 || "交通银行 · 总行 · "+row.Department != r.Company {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	r.Locations = splitPlaces(row.City)
	text, err := bankPostingText(r, "交通银行总行 · "+bankcommITProject+"（2026 指招聘季，毕业范围见原文；官网入口需筛选总行）", row.Duties, row.Requirements)
	if err == nil && row.Deadline != "" {
		text += "\n官网公布截止日期：" + row.Deadline
	}
	return text, err
}
