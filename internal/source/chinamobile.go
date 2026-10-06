package source

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const mobileOrigin = "https://job.10086.cn"

var mobileUnits = map[string]struct{ ID, Name, Company string }{
	"cmcloud": {"79", "云公司", "中移（苏州）软件技术有限公司"},
	"cmiot":   {"77", "物联网公司", "中移物联网有限公司"},
	"cmhome":  {"81", "智慧家庭运营中心", "中国移动智慧家庭运营中心"},
}

func mobileCampusURL(adapter string) string {
	return mobileOrigin + "/personal/campus/campus_job_list.html?cId=" + mobileUnits[adapter].ID
}
func mobileJobURL(id string) string {
	return mobileOrigin + "/personal/job/detail.html?" + url.Values{"id": {id}, "typess": {"1"}}.Encode()
}

// Published RSA public key from /js/job-center.js. This is the anonymous
// read-query nonce protocol used by the public campus listing, not a candidate
// credential. MD5 is required by that protocol; it is not used for security.
const mobilePublicKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAhbieIVi00W3W1i9hYVs1EY6iYLF936QV71fmFNtsATK3m7iEbgDNo222M2uRJ1fVFyt00OkwyJ/EzvLL7M2iWK7d3fs8OAwsJd0/tBGhFvJU9YUzGibvko3KfOiUr+CMLwrGY4cXyPUs/DHiwqVb+/JhvffKTzzpZxnmOZDY5G7q6FfLFmGueQI7h9NyqyTst1jrfJRq2QG2uDDuMNlYEjWNSHI7fg9F91xLhyNNKIO1a3dcpLi8HZEtm4mgs1+i2xH49EzVjLyFjep91nqNUrauXVr22DMGfuggeAzuRxlqo1bVNg9pC1EtcTg4GkWURf4FWngXo4ntHpGcd+hecwIDAQAB"

type mobileHeader struct {
	Version      string `json:"version"`
	Timestamp    int64  `json:"timestamp"`
	Digest       string `json:"digest"`
	Conversation string `json:"conversationId"`
}

func newMobileHeader() (mobileHeader, error) {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	nonce := make([]byte, 10)
	for i := range nonce {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return mobileHeader{}, err
		}
		nonce[i] = alphabet[n.Int64()]
	}
	der, err := base64.StdEncoding.DecodeString(mobilePublicKey)
	if err != nil {
		return mobileHeader{}, err
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return mobileHeader{}, err
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return mobileHeader{}, fmt.Errorf("invalid public query key")
	}
	cipher, err := rsa.EncryptPKCS1v15(rand.Reader, pub, nonce)
	if err != nil {
		return mobileHeader{}, err
	}
	now := time.Now()
	stamp := now.UnixMilli()
	sum := md5.Sum([]byte(strconv.FormatInt(stamp, 10) + string(nonce)))
	digest := base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(sum[:]))) + ";" + base64.StdEncoding.EncodeToString(cipher)
	serial, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return mobileHeader{}, err
	}
	conversation := now.Format("20060102150405") + fmt.Sprintf("%03d%06d", now.Nanosecond()/1000000, serial.Int64())
	return mobileHeader{"1.0", stamp, digest, conversation}, nil
}
func (a PublicPlatform) mobileQuery(ctx context.Context, s d.Source, path, service string, data any, dst any) error {
	header, err := newMobileHeader()
	if err != nil {
		return fail("SCHEMA_INVALID", false, 0)
	}
	return a.post(ctx, s, mobileOrigin+path, struct {
		Service string       `json:"serviceName"`
		Header  mobileHeader `json:"header"`
		Data    any          `json:"data"`
	}{service, header, data}, dst)
}

// Only the two verified public list queries return JSON labelled text/plain.
// Do not relax MIME checks for other sources, paths, HTML or credential APIs.
func mobilePlainJSON(adapter, method, path, media string) bool {
	_, ok := mobileUnits[adapter]
	return ok && method == http.MethodPost && media == "text/plain" && (path == "/job-app/company/getCompanyList.do" || path == "/job-app/job/searchJobs.do")
}

type mobileEnvelope[T any] struct {
	Code string `json:"code"`
	Data T      `json:"data"`
}

// The public client calls JSON.parse on this response: depending on content
// negotiation the portal emits either a JSON object or a JSON-encoded string.
// Permit exactly one string wrapper for this envelope, without changing how
// other providers decode their bodies.
func (v *mobileEnvelope[T]) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var inner string
		if err := json.Unmarshal(raw, &inner); err != nil {
			return err
		}
		raw = bytes.TrimSpace([]byte(inner))
	}
	if len(raw) == 0 || raw[0] != '{' {
		return fmt.Errorf("invalid public query envelope")
	}
	type envelope mobileEnvelope[T]
	return json.Unmarshal(raw, (*envelope)(v))
}

type mobileJob struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Company     string `json:"company"`
	ShortName   string `json:"companyShortName"`
	Type        string `json:"type"`
	WorkType    string `json:"workType"`
	City        string `json:"city"`
	Province    string `json:"province"`
	Description string `json:"description"`
	Conditions  string `json:"dutyCondition"`
	Start       string `json:"startTime"`
	End         string `json:"endTime"`
}

func mobileRef(adapter string, j mobileJob) (PostingRef, error) {
	unit := mobileUnits[adapter]
	if !baiduPostID.MatchString(j.ID) || j.Type != "1" || j.WorkType != "02" || j.Company != unit.Company || j.ShortName != unit.Name {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	places := splitPlaces(j.City)
	if len(places) == 0 {
		places = splitPlaces(j.Province)
	}
	r := PostingRef{ExternalID: j.ID, URL: mobileJobURL(j.ID), Title: j.Name, Company: j.Company, JobType: "FULL_TIME", Locations: places}
	return r, validateRef(r)
}
func (a PublicPlatform) verifyMobile(ctx context.Context, s d.Source) error {
	var v mobileEnvelope[struct {
		Companies []struct {
			ID   string `json:"companyId"`
			Name string `json:"shortName"`
			Type *int   `json:"type"`
		} `json:"companyList"`
	}]
	if err := a.mobileQuery(ctx, s, "/job-app/company/getCompanyList.do", "getCompanyList", map[string]any{"newJob": 1}, &v); err != nil {
		return err
	}
	unit := mobileUnits[s.Adapter]
	matches := 0
	for _, company := range v.Data.Companies {
		if company.ID == unit.ID && company.Name == unit.Name && company.Type != nil && *company.Type == 3 {
			matches++
		}
	}
	if v.Code != "0000" || matches != 1 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) mobileRows(ctx context.Context, s d.Source) ([]PostingRef, map[string]mobileJob, error) {
	rows := map[string]mobileJob{}
	refs, err := campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v mobileEnvelope[struct {
			Total *int        `json:"total"`
			Jobs  []mobileJob `json:"jobList"`
		}]
		data := map[string]any{"pageNo": page, "pageSize": 20, "companyId": mobileUnits[s.Adapter].ID, "type": 1, "category": "", "workYear": "", "degree": "", "workType": "", "workProvince": "", "workCity": ""}
		if err := a.mobileQuery(ctx, s, "/job-app/job/searchJobs.do", "searchJobs", data, &v); err != nil {
			return campusPage{}, err
		}
		if v.Code != "0000" || v.Data.Total == nil || v.Data.Jobs == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 20}
		for _, j := range v.Data.Jobs {
			r, err := mobileRef(s.Adapter, j)
			if err != nil {
				return out, err
			}
			rows[j.ID] = j
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return refs, rows, nil
}
func (a PublicPlatform) discoverMobile(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.verifyMobile(ctx, s); err != nil {
		return nil, err
	}
	refs, _, err := a.mobileRows(ctx, s)
	return refs, err
}
func (a PublicPlatform) fetchMobile(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !baiduPostID.MatchString(r.ExternalID) || r.URL != mobileJobURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	// The verified public listing already publishes the full original fields.
	// Re-read complete pages and bind by ID/title/unit; do not guess a detail API
	// or use candidate login, application or eligibility endpoints.
	_, rows, err := a.mobileRows(ctx, s)
	if err != nil {
		return "", err
	}
	j, ok := rows[r.ExternalID]
	if !ok || j.Name != r.Title {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	actual, err := mobileRef(s.Adapter, j)
	if err != nil {
		return "", err
	}
	description, conditions := plainHTML(j.Description), plainHTML(j.Conditions)
	if strings.TrimSpace(description) == "" && strings.TrimSpace(conditions) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text := fmt.Sprintf("岗位名称：%s\n公司：%s\n招聘类型：校园招聘全职\n招聘单位：%s\n工作地点：%s", actual.Title, actual.Company, mobileUnits[s.Adapter].Name, strings.Join(actual.Locations, "、"))
	// Units use these columns inconsistently. Preserve their wording instead of
	// classifying description as duties or treating an empty second column as
	// missing requirements (the cloud unit publishes requirements in the first).
	if strings.TrimSpace(description) != "" {
		text += "\n职位描述（官网原文）：\n" + description
	}
	if strings.TrimSpace(conditions) != "" {
		text += "\n补充条件（官网原文）：\n" + conditions
	}
	return text, nil
}
