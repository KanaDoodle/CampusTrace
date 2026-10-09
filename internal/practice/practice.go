// Package practice executes only explicit Go exercises through a separate trusted runner.
package practice

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const Policy = "go-practice-v1"
const Image = "golang:1.25.9-bookworm"
const Deadline = 45 * time.Second
const MaxOutput = 16000

var ErrInvalid = errors.New("invalid Go exercise")
var ErrUnavailable = errors.New("practice runner unavailable")

type Input struct {
	Code       string `json:"code"`
	Tests      string `json:"tests"`
	RequestKey string `json:"request_key"`
}
type Request struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Tests string `json:"tests"`
}
type Result struct {
	State      string `json:"state"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
	Policy     string `json:"policy"`
}

// Syntax/import validation is a usability check. Isolation is enforced by Docker,
// not by the parser: even stdlib code can be hostile.
func Validate(code, tests string) error {
	if len(code) == 0 || len(code) > 24000 || len(tests) == 0 || len(tests) > 24000 || !utf8.ValidString(code) || !utf8.ValidString(tests) {
		return ErrInvalid
	}
	testCount := 0
	for i, src := range []string{code, tests} {
		f, err := parser.ParseFile(token.NewFileSet(), "exercise.go", src, parser.AllErrors)
		if err != nil || f.Name.Name != "exercise" {
			return ErrInvalid
		}
		for _, im := range f.Imports {
			path, err := strconv.Unquote(im.Path.Value)
			if err != nil || !allowedImport[path] {
				return ErrInvalid
			}
		}
		if i == 1 {
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") && fn.Name.Name != "TestMain" {
					testCount++
				}
			}
		}
	}
	if testCount == 0 {
		return ErrInvalid
	}
	return nil
}

var allowedImport = map[string]bool{}

func init() {
	for _, s := range strings.Fields("bytes cmp container/heap container/list container/ring context crypto/sha256 encoding/hex encoding/json errors fmt io math math/rand math/rand/v2 net net/http net/url os path path/filepath reflect regexp runtime slices sort strconv strings sync sync/atomic testing time unicode unicode/utf8") {
		allowedImport[s] = true
	}
}
func signature(key []byte, stamp, method, path string, body []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(Policy + "\n" + stamp + "\n" + method + "\n" + path + "\n"))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}
func Authorize(r *http.Request, key []byte, body []byte) bool {
	stamp := r.Header.Get("X-Practice-Time")
	n, e := strconv.ParseInt(stamp, 10, 64)
	if e != nil || len(key) < 32 || time.Since(time.Unix(n, 0)) > time.Minute || time.Until(time.Unix(n, 0)) > time.Minute {
		return false
	}
	got, e := hex.DecodeString(r.Header.Get("X-Practice-Signature"))
	want, _ := hex.DecodeString(signature(key, stamp, r.Method, r.URL.Path, body))
	return e == nil && hmac.Equal(got, want)
}

type Client struct {
	URL  string
	Key  []byte
	HTTP *http.Client
}

func (c *Client) call(ctx context.Context, path string, v any, out any) error {
	if c == nil || c.URL == "" || len(c.Key) < 32 {
		return ErrUnavailable
	}
	var b []byte
	var err error
	if v != nil {
		b, err = json.Marshal(v)
		if err != nil {
			return err
		}
	}
	method := http.MethodPost
	if v == nil {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Practice-Time", stamp)
	req.Header.Set("X-Practice-Signature", signature(c.Key, stamp, method, path, b))
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: Deadline + 5*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := h.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return ErrUnavailable
	}
	b, err = io.ReadAll(io.LimitReader(res.Body, MaxOutput*4+1000))
	if err != nil {
		return ErrUnavailable
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
func (c *Client) Ready(ctx context.Context) bool {
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return c.call(probe, "/readyz", nil, nil) == nil
}
func (c *Client) Run(ctx context.Context, v Request) (Result, error) {
	var out Result
	e := c.call(ctx, "/run", v, &out)
	if e == nil && out.Policy != Policy {
		e = ErrUnavailable
	}
	return out, e
}
