package practice

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const code = "package exercise\nfunc Add(a,b int)int{return a+b}"
const tests = "package exercise\nimport \"testing\"\nfunc TestAdd(t *testing.T){if Add(1,2)!=3{t.Fatal(\"sum\")}}"

func TestValidationAndSignedBoundary(t *testing.T) {
	if Validate(code, tests) != nil {
		t.Fatal("valid exercise")
	}
	for _, bad := range []string{"package main", "package exercise\nimport \"example.org/private\"", strings.Repeat("x", 24001)} {
		if Validate(bad, tests) == nil {
			t.Fatal("accepted", bad[:10])
		}
	}
	if Validate(code, "package exercise") == nil {
		t.Fatal("test required")
	}
	key := []byte(strings.Repeat("k", 32))
	body := []byte(`{"id":"example"}`)
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest("POST", "/run", bytes.NewReader(body))
	req.Header.Set("X-Practice-Time", stamp)
	req.Header.Set("X-Practice-Signature", signature(key, stamp, "POST", "/run", body))
	if !Authorize(req, key, body) || Authorize(req, key, []byte("tampered")) {
		t.Fatal("signature")
	}
	req.URL.Path = "/other"
	if Authorize(req, key, body) {
		t.Fatal("path unbound")
	}
	req.URL.Path = "/run"
	old := strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)
	req.Header.Set("X-Practice-Time", old)
	req.Header.Set("X-Practice-Signature", signature(key, old, "POST", "/run", body))
	if Authorize(req, key, body) {
		t.Fatal("expired signature")
	}
}
func TestOutputIsBoundedAndDemultiplexed(t *testing.T) {
	var b bytes.Buffer
	for _, s := range []string{"stdout\n", strings.Repeat("界", 10000)} {
		h := make([]byte, 8)
		h[0] = 1
		binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
		b.Write(h)
		b.WriteString(s)
	}
	out, truncated, e := readOutput(&b)
	if e != nil || !truncated || len(out) > MaxOutput || !strings.HasPrefix(out, "stdout") {
		t.Fatal(len(out), truncated, e)
	}
}
func TestDockerCleanupEvenWhenExecFails(t *testing.T) {
	var removed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE":
			removed.Store(true)
			w.WriteHeader(204)
		case r.URL.Path == "/v1.47/containers/create":
			w.Write([]byte(`{"Id":"container"}`))
		case strings.HasSuffix(r.URL.Path, "/exec"):
			w.Write([]byte(`{"Id":"exec"}`))
		case r.URL.Path == "/v1.47/exec/exec/start":
			w.WriteHeader(500)
		default:
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	d := NewDocker("unused", Image, "synthetic")
	d.HTTP = &http.Client{Transport: rewriteTransport{base: server.URL}}
	_, err := d.Run(context.Background(), Request{ID: strings.Repeat("a", 32), Code: code, Tests: tests})
	if err == nil || !removed.Load() {
		t.Fatal("cleanup not enforced", err, removed.Load())
	}
}

type rewriteTransport struct{ base string }

func (r rewriteTransport) RoundTrip(q *http.Request) (*http.Response, error) {
	q = q.Clone(q.Context())
	q.URL.Scheme = "http"
	q.URL.Host = strings.TrimPrefix(r.base, "http://")
	return http.DefaultTransport.RoundTrip(q)
}
func TestRealGoPracticeIsolation(t *testing.T) {
	if os.Getenv("CAMPUS_SANDBOX_INTEGRATION") != "1" {
		t.Skip("explicit opt-in Docker exercise")
	}
	d := NewDocker("/var/run/docker.sock", Image, "campustrace-test")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	input := Request{ID: strings.Repeat("b", 32), Code: code, Tests: `package exercise
import("testing";"os";"net";"time")
func TestIsolation(t *testing.T){
 if Add(1,2)!=3{t.Fatal("sum")}
 if os.Geteuid()==0{t.Fatal("root")}
 if os.Getenv("JWT_SECRET")!=""||os.Getenv("LLM_API_KEY")!=""{t.Fatal("secrets")}
 if _,err:=os.Stat("/var/run/docker.sock");err==nil{t.Fatal("Docker socket exposed")}
 if err:=os.WriteFile("/etc/practice-escape",[]byte("x"),0600);err==nil{t.Fatal("writable root")}
 if c,err:=net.DialTimeout("tcp","1.1.1.1:80",150*time.Millisecond);err==nil{c.Close();t.Fatal("network exposed")}
}`}
	out, e := d.Run(ctx, input)
	if e != nil || out.State != "PASSED" {
		t.Fatal(e, out)
	}
	t.Logf("real Go isolation state=%s duration_ms=%d output=%q", out.State, out.DurationMS, out.Output)
	// A failing assertion must remain FAILED, not become a successful receipt.
	input.ID = strings.Repeat("c", 32)
	input.Tests = tests + "\nfunc TestFailure(t *testing.T){t.Fatal(\"deliberate failure\")}"
	out, e = d.Run(ctx, input)
	if e != nil || out.State != "FAILED" || !strings.Contains(out.Output, "deliberate failure") {
		t.Fatal(e, out)
	}
	input.ID = strings.Repeat("d", 32)
	input.Tests = "package exercise\nimport (\"testing\";\"time\")\nfunc TestTimeout(t *testing.T){time.Sleep(15*time.Second)}"
	out, e = d.Run(ctx, input)
	if e != nil || out.State != "TIMEOUT" {
		t.Fatal("timeout classification", e, out)
	}

}
