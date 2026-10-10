package transport

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
)

const sessionCookie = "campustrace-session"

func (a *API) sessions() auth.Sessions {
	if a.Queue == nil {
		return auth.Sessions{}
	}
	return auth.Sessions{Redis: a.Queue.R, Prefix: a.Queue.Prefix}
}

// Cookies accompany requests automatically, so authenticated writes must come
// from this origin. JSON CLI requests without browser headers remain usable.
func sameOrigin(r *http.Request, required bool) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return !required
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, expiry time.Time, remember bool) {
	cookie := &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode}
	if remember {
		cookie.MaxAge = int(auth.RememberLifetime / time.Second)
		cookie.Expires = expiry
	}
	http.SetCookie(w, cookie)
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func (a *API) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			codedError(w, http.StatusUnauthorized, "SESSION_EXPIRED")
			return
		}
		id, err := a.sessions().Verify(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, auth.ErrSession) {
				codedError(w, http.StatusUnauthorized, "SESSION_EXPIRED")
			} else {
				codedError(w, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE")
			}
			return
		}
		write(w, map[string]any{"authenticated": true, "user_id": id}, nil)
	})
	mux.HandleFunc("POST /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !sameOrigin(r, false) {
			codedError(w, http.StatusForbidden, "AUTH_ORIGIN_INVALID")
			return
		}
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			if !sameOrigin(r, true) {
				codedError(w, http.StatusForbidden, "AUTH_ORIGIN_INVALID")
				return
			}
			if err := a.sessions().Revoke(r.Context(), cookie.Value); err != nil {
				codedError(w, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE")
				return
			}
		}
		clearSessionCookie(w, r)
		write(w, map[string]bool{"logged_out": true}, nil)
	})
}
