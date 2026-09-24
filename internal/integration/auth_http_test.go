package integration

import (
	"bytes"
	"encoding/json"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http/httptest"
	"testing"
)

// Registration is the first screen a new user meets. A taken email used to be
// reported as a generic 400, which the UI rendered as "提交未成功，请检查必填项和
// 填写格式后重试" and sent people editing a perfectly valid form.
func TestRegisterDuplicateEmailReportsConflict(t *testing.T) {
	_, s, q, _, _ := setup(t)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-auth-http-secret-32-bytes")}
	api := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	email := d.ID() + "@dup-register.invalid"
	register := func() *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]string{"email": email, "password": "duplicate-password-2027"})
		must(t, err)
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("POST", "/auth/register", bytes.NewReader(body)))
		return rec
	}
	if rec := register(); rec.Code != 200 {
		t.Fatalf("first registration status %d body %s", rec.Code, rec.Body.String())
	}
	rec := register()
	if rec.Code != 409 {
		t.Fatalf("duplicate registration status %d, want 409", rec.Code)
	}
	var body map[string]string
	must(t, json.Unmarshal(rec.Body.Bytes(), &body))
	if body["code"] != "EMAIL_TAKEN" {
		t.Fatalf("duplicate registration code %q, want EMAIL_TAKEN", body["code"])
	}
	// The email is only special at registration; logging in still works.
	login, err := json.Marshal(map[string]string{"email": email, "password": "duplicate-password-2027"})
	must(t, err)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/auth/login", bytes.NewReader(login)))
	if rec.Code != 200 {
		t.Fatalf("login for the existing account status %d body %s", rec.Code, rec.Body.String())
	}
}
