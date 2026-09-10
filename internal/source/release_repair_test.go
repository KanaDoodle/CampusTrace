package source

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseContentTypeAndDecompressedLimit(t *testing.T) {
	for _, tc := range []struct{ media, encoding, body, want string }{
		{"text/plain", "", "Apply now", "SUCCESS"}, {"text/html; charset=utf-8", "", "<p>Apply now</p>", "SUCCESS"},
		{"application/octet-stream", "", "Apply now", "PARSE_ERROR"}, {"image/png", "", "Apply now", "PARSE_ERROR"},
		{"text/html", "gzip", "<p>Apply now</p><script>" + strings.Repeat("x", 1<<20) + "</script><p>Applications are closed</p>", "PARSE_ERROR"},
	} {
		t.Run(tc.media+tc.encoding, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.media)
				if tc.encoding != "" {
					w.Header().Set("Content-Encoding", tc.encoding)
					gz := gzip.NewWriter(w)
					gz.Write([]byte(tc.body))
					gz.Close()
				} else {
					w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()
			tr := http.DefaultTransport.(*http.Transport).Clone()
			tr.DisableCompression = true
			defer tr.CloseIdleConnections()
			r, err := (HTTPAdapter{Client: &http.Client{Transport: tr}}).Fetch(context.Background(), srv.URL)
			if err != nil || r.Status != tc.want {
				t.Fatal(r.Status, err)
			}
			if r.Status != "SUCCESS" && r.Text != "" {
				t.Fatal("incomplete content escaped")
			}
		})
	}
}
