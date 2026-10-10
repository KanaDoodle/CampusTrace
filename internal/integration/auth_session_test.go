package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"golang.org/x/crypto/bcrypt"
)

func TestBrowserSessionsRestoreExpireRotateAndRevoke(t *testing.T) {
	ctx, s, q, _, _ := setup(t)
	api := (&transport.API{Store: s, Queue: q, Auth: auth.Service{Store: s, Secret: []byte("synthetic-auth-session-secret-32-bytes")}, Metrics: observability.New()}).Handler()
	request := func(method, path string, body any, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			must(t, err)
		}
		r := httptest.NewRequest(method, "https://campus.invalid"+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	for _, remember := range []bool{false, true} {
		email := d.ID() + "@session.invalid"
		credentials := map[string]any{"email": email, "password": "go123456"}
		if w := request("POST", "/auth/register", credentials, nil, "https://campus.invalid"); w.Code != 200 {
			t.Fatalf("register: %d %s", w.Code, w.Body)
		}
		credentials["web_session"], credentials["remember"] = true, remember
		login := request("POST", "/auth/login", credentials, nil, "https://campus.invalid")
		if login.Code != 200 || strings.Contains(login.Body.String(), "token") || strings.Contains(login.Body.String(), "go123456") {
			t.Fatalf("cookie login: %d %s", login.Code, login.Body)
		}
		cookies := login.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected one cookie, got %d", len(cookies))
		}
		cookie := cookies[0]
		if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
			t.Fatalf("cookie attributes: %+v", cookie)
		}
		expectedTTL := auth.SessionLifetime
		if remember {
			expectedTTL = auth.RememberLifetime
			if cookie.MaxAge != int(expectedTTL/time.Second) || cookie.Expires.IsZero() {
				t.Fatal("remembered cookie is not persistent")
			}
		} else if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
			t.Fatal("ordinary cookie must stay a browser session cookie")
		}
		hash := sha256.Sum256([]byte(cookie.Value))
		key := q.Prefix + "auth:session:" + hex.EncodeToString(hash[:])
		ttl, err := q.R.TTL(ctx, key).Result()
		must(t, err)
		if ttl > expectedTTL || ttl < expectedTTL-time.Minute {
			t.Fatalf("session TTL %s, expected %s", ttl, expectedTTL)
		}
		for _, path := range []string{"/auth/session", "/api/projects"} {
			if w := request("GET", path, nil, cookie, ""); w.Code != 200 {
				t.Fatalf("restore/read %s: %d %s", path, w.Code, w.Body)
			}
		}
		for _, origin := range []string{"", "null", "https://evil.invalid", "http://campus.invalid"} {
			w := request("POST", "/api/projects", map[string]string{"name": "blocked"}, cookie, origin)
			if w.Code != 403 {
				t.Fatalf("cookie write origin %q: %d", origin, w.Code)
			}
		}
		if w := request("POST", "/api/projects", map[string]string{"name": "session-owned-project"}, cookie, "https://campus.invalid"); w.Code != 200 {
			t.Fatalf("same-origin write: %d %s", w.Code, w.Body)
		}
		// Rotating on login makes the previous browser verifier unusable.
		rotated := request("POST", "/auth/login", credentials, cookie, "https://campus.invalid")
		if rotated.Code != 200 {
			t.Fatalf("rotate: %d %s", rotated.Code, rotated.Body)
		}
		fresh := rotated.Result().Cookies()[0]
		if fresh.Value == cookie.Value || request("GET", "/auth/session", nil, cookie, "").Code != 401 {
			t.Fatal("old verifier survived rotation")
		}
		if w := request("POST", "/auth/logout", nil, fresh, "https://evil.invalid"); w.Code != 403 {
			t.Fatal("cross-origin logout succeeded")
		}
		if request("GET", "/auth/session", nil, fresh, "").Code != 200 {
			t.Fatal("blocked logout revoked the session")
		}
		logout := request("POST", "/auth/logout", nil, fresh, "https://campus.invalid")
		if logout.Code != 200 || logout.Result().Cookies()[0].MaxAge != -1 || request("GET", "/api/projects", nil, fresh, "").Code != 401 {
			t.Fatal("logged-out cookie can still be replayed")
		}
		last := request("POST", "/auth/login", credentials, nil, "https://campus.invalid").Result().Cookies()[0]
		hash = sha256.Sum256([]byte(last.Value))
		must(t, q.R.Expire(ctx, q.Prefix+"auth:session:"+hex.EncodeToString(hash[:]), 0).Err())
		if request("GET", "/auth/session", nil, last, "").Code != 401 {
			t.Fatal("expired Redis session restored")
		}
	}
}

func TestRegistrationByteLimitsAndLegacyPasswordLogin(t *testing.T) {
	ctx, s, q, _, _ := setup(t)
	service := auth.Service{Store: s, Secret: []byte("synthetic-auth-session-secret-32-bytes")}
	for _, v := range []struct {
		password string
		valid    bool
	}{
		{"1234567", false}, {"12345678", true}, {strings.Repeat("a", 20), true}, {strings.Repeat("a", 21), false}, {"汉字好", true}, {strings.Repeat("汉", 7), false},
	} {
		_, err := service.Register(ctx, d.ID()+"@password.invalid", v.password)
		if (err == nil) != v.valid {
			t.Fatalf("password bytes=%d valid=%v err=%v", len(v.password), v.valid, err)
		}
	}
	long := "legacy-password-more-than-20"
	hash, err := bcrypt.GenerateFromPassword([]byte(long), bcrypt.DefaultCost)
	must(t, err)
	email := d.ID() + "@legacy-login.invalid"
	owner, err := s.NewUser(ctx, email, string(hash))
	must(t, err)
	got, err := service.Authenticate(ctx, email, long)
	must(t, err)
	if got != owner {
		t.Fatal("legacy account could not log in")
	}
	api := (&transport.API{Store: s, Queue: q, Auth: service, Metrics: observability.New()}).Handler()
	data, err := json.Marshal(map[string]string{"email": d.ID() + "@invalid-password.invalid", "password": "short"})
	must(t, err)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest("POST", "/auth/register", bytes.NewReader(data)))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "PASSWORD_LENGTH_INVALID") {
		t.Fatalf("invalid registration response: %d %s", w.Code, w.Body)
	}
}
