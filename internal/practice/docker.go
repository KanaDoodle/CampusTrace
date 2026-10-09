package practice

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type Docker struct {
	HTTP  *http.Client
	Image string
	Owner string
}

func NewDocker(socket, image, owner string) *Docker {
	return &Docker{Image: image, Owner: owner, HTTP: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}}
}
func (d *Docker) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, method, "http://docker/v1.47"+path, body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.Contains(path, "/archive") {
		req.Header.Set("Content-Type", "application/x-tar")
	}
	res, e := d.HTTP.Do(req)
	if e != nil {
		return nil, e
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 2000))
		res.Body.Close()
		return nil, fmt.Errorf("docker API %s %s status %d: %s", method, path, res.StatusCode, detail)
	}
	return res, nil
}
func (d *Docker) call(ctx context.Context, method, path string, v, out any) error {
	var b []byte
	if v != nil {
		b, _ = json.Marshal(v)
	}
	res, e := d.request(ctx, method, path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
	}
	return nil
}
func (d *Docker) Ready(ctx context.Context) error {
	return d.call(ctx, "GET", "/images/"+url.PathEscape(d.Image)+"/json", nil, nil)
}
func (d *Docker) profile(id string) map[string]any {
	return map[string]any{"Image": d.Image, "User": "65532:65532", "WorkingDir": "/workspace", "Cmd": []string{"/bin/sleep", "55"}, "Entrypoint": []string{}, "NetworkDisabled": true,
		"Env":    []string{"PATH=/usr/local/go/bin:/usr/bin:/bin", "HOME=/tmp", "GOCACHE=/tmp/cache", "GOPATH=/tmp/gopath", "GOMAXPROCS=2", "GOFLAGS=-p=2", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"},
		"Labels": map[string]string{"campustrace.practice": d.Owner, "campustrace.run_id": id},
		"HostConfig": map[string]any{"NetworkMode": "none", "ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "Memory": 384 * 1024 * 1024, "MemorySwap": 384 * 1024 * 1024, "NanoCpus": 1000000000, "PidsLimit": 128,
			"Tmpfs": map[string]string{"/workspace": "rw,noexec,nosuid,nodev,size=16m,uid=65532,gid=65532,mode=0700", "/tmp": "rw,exec,nosuid,nodev,size=256m,mode=1777"}, "LogConfig": map[string]any{"Type": "none"}, "Ulimits": []map[string]any{{"Name": "nofile", "Soft": 256, "Hard": 256}}}}
}
func archive(v Request) []byte {
	var b bytes.Buffer
	t := tar.NewWriter(&b)
	for _, f := range []struct{ name, text string }{{"go.mod", "module exercise\n\ngo 1.25\n"}, {"exercise.go", v.Code}, {"exercise_test.go", v.Tests}} {
		t.WriteHeader(&tar.Header{Name: f.name, Size: int64(len(f.text)), Mode: 0600, Uid: 65532, Gid: 65532})
		t.Write([]byte(f.text))
	}
	t.Close()
	return b.Bytes()
}

// Docker multiplexes stdout/stderr using an 8-byte header. Retain bounded UTF-8
// output while draining the stream, so a verbose exercise cannot grow memory.
func readOutput(r io.Reader) (string, bool, error) {
	var b bytes.Buffer
	truncated := false
	var header [8]byte
	buf := make([]byte, 4096)
	for {
		_, err := io.ReadFull(r, header[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return b.String(), truncated, err
		}
		n := int64(binary.BigEndian.Uint32(header[4:]))
		if n > 64<<20 {
			return b.String(), true, errors.New("invalid log frame")
		}
		for n > 0 {
			k := int64(len(buf))
			if k > n {
				k = n
			}
			count, e := io.ReadFull(r, buf[:k])
			left := MaxOutput - b.Len()
			if count > left {
				truncated = true
			}
			if left > 0 {
				if count < left {
					left = count
				}
				b.Write(buf[:left])
			}
			n -= int64(count)
			if e != nil {
				return b.String(), truncated, e
			}
		}
	}
	s := strings.ToValidUTF8(b.String(), "�")
	for len(s) > MaxOutput {
		s = s[:len(s)-1]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s, truncated, nil
}
func (d *Docker) Run(parent context.Context, v Request) (out Result, err error) {
	start := time.Now()
	out = Result{State: "ERROR", ExitCode: -1, Policy: Policy}
	defer func() { out.DurationMS = time.Since(start).Milliseconds() }()
	if len(v.ID) != 32 || Validate(v.Code, v.Tests) != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, Deadline)
	defer cancel()
	var container struct {
		ID string `json:"Id"`
	}
	if err = d.call(ctx, "POST", "/containers/create", d.profile(v.ID), &container); err != nil {
		return
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if e := d.call(cleanup, "DELETE", "/containers/"+container.ID+"?force=true&v=true", nil, nil); e != nil {
			err = errors.Join(err, e)
			out.State = "ERROR"
		}
	}()
	if err = d.call(ctx, "POST", "/containers/"+container.ID+"/start", nil, nil); err != nil {
		return
	}

	// Payload is a separate encoded argument, never interpolated into shell code.
	// Docker archive uploads reject a read-only root even for writable tmpfs.
	var exec struct {
		ID string `json:"Id"`
	}
	err = d.call(ctx, "POST", "/containers/"+container.ID+"/exec", map[string]any{"AttachStdout": true, "AttachStderr": true, "Tty": false, "User": "65532:65532", "WorkingDir": "/workspace", "Cmd": []string{"/bin/sh", "-c", `printf '%s' "$1" | base64 -d | tar -xf - -C /workspace && exec /usr/local/go/bin/go test -count=1 -timeout=12s ./...`, "practice", base64.StdEncoding.EncodeToString(archive(v))}}, &exec)
	if err != nil {
		return
	}
	res, e := d.request(ctx, "POST", "/exec/"+exec.ID+"/start", strings.NewReader(`{"Detach":false,"Tty":false}`))
	if e == nil {
		out.Output, out.Truncated, e = readOutput(res.Body)
		res.Body.Close()
	}
	if ctx.Err() != nil {
		out.State = "TIMEOUT"
		if errors.Is(ctx.Err(), context.Canceled) {
			out.State = "INTERRUPTED"
		}
		err = nil
		return
	}
	if e != nil {
		err = e
		return
	}
	var state struct {
		Running  bool
		ExitCode int
	}
	if err = d.call(ctx, "GET", "/exec/"+exec.ID+"/json", nil, &state); err != nil {
		return
	}
	var inspected struct{ State struct{ OOMKilled bool } }
	if err = d.call(ctx, "GET", "/containers/"+container.ID+"/json", nil, &inspected); err != nil {
		return
	}
	out.ExitCode = state.ExitCode
	out.State = "FAILED"
	if inspected.State.OOMKilled {
		out.State = "OOM"
	} else if strings.Contains(out.Output, "test timed out after") {
		out.State = "TIMEOUT"
	} else if !state.Running && state.ExitCode == 0 {
		out.State = "PASSED"
	}
	return
}

// Clean only this controller's abandoned containers on startup. The fixed parent
// lifetime also prevents an orphan exercise from running indefinitely.
func (d *Docker) Cleanup(ctx context.Context) error {
	filters, _ := json.Marshal(map[string][]string{"label": {"campustrace.practice=" + d.Owner}})
	var rows []struct {
		ID string `json:"Id"`
	}
	if e := d.call(ctx, "GET", "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil, &rows); e != nil {
		return e
	}
	for _, v := range rows {
		if e := d.call(ctx, "DELETE", "/containers/"+v.ID+"?force=true&v=true", nil, nil); e != nil {
			return e
		}
	}
	return nil
}
