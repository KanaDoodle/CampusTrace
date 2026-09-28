package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeComposeStatus(t *testing.T) {
	for _, input := range []string{`[{"Service":"api","State":"running","Health":"healthy"}]`, `{"Service":"api","State":"running","Health":"healthy"}` + "\n"} {
		rows, err := decodeContainers([]byte(input))
		if err != nil || len(rows) != 1 || rows[0].Service != "api" {
			t.Fatal(rows, err)
		}
	}
	if _, err := decodeContainers([]byte("not JSON")); err == nil {
		t.Fatal("accepted invalid status")
	}
	if err := ready([]Container{{Service: "api", State: "running", Health: "healthy"}}); err == nil {
		t.Fatal("accepted missing worker and dependencies")
	}
	rows := healthy()
	rows[0].Health = "unhealthy"
	if ready(rows) == nil {
		t.Fatal("accepted unhealthy component")
	}
}
func healthy() []Container {
	rows := []Container{}
	for _, s := range allServices {
		rows = append(rows, Container{Service: s, State: "running", Health: "healthy"})
	}
	return rows
}
func unusedPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	listener.Close()
	return port
}
func TestFailedStartOnlyStopsNewApplicationServices(t *testing.T) {
	var calls [][]string
	port := unusedPort(t)
	before := healthy()[:3]
	before = append(before, Container{Service: "api", State: "running", Health: "healthy"})
	states, _ := json.Marshal(before)
	m := manager{root: t.TempDir(), out: io.Discard, errOut: io.Discard, execute: func(_ context.Context, _ string, args []string, out, _ io.Writer) error {
		calls = append(calls, append([]string{}, args...))
		line := strings.Join(args, " ")
		switch {
		case args[0] == "launchctl":
			return errors.New("no legacy job")
		case strings.Contains(line, "ps --all --format json"):
			out.Write(states)
		case strings.Contains(line, "config --format json"):
			io.WriteString(out, `{"services":{"api":{"ports":[{"published":"`+port+`"}]}}}`)
		case strings.Contains(line, "up --detach"):
			return errors.New("simulated failure")
		}
		return nil
	}}
	if m.start(context.Background(), false, false, 1) == nil {
		t.Fatal("accepted failed startup")
	}
	stop := []string{}
	for _, args := range calls {
		for i, v := range args {
			if v == "stop" {
				stop = args[i+1:]
			}
		}
		for _, v := range args {
			if v == "down" || v == "seed" || v == "--volumes" {
				t.Fatal("unsafe cleanup", args)
			}
		}
	}
	if !reflect.DeepEqual(stop, []string{"worker", "analysis-1", "analysis-2"}) {
		t.Fatal("rollback stopped preexisting service or dependency", stop)
	}
}
func TestStartUsesDetachedComposeAndExplicitProjectFromAnyDirectory(t *testing.T) {
	var calls [][]string
	rows, _ := json.Marshal(healthy())
	port := unusedPort(t)
	root := filepath.Join(t.TempDir(), "project with spaces")
	m := manager{root: root, out: io.Discard, errOut: io.Discard, execute: func(_ context.Context, dir string, args []string, out, _ io.Writer) error {
		if dir != root {
			t.Fatal("wrong working directory")
		}
		calls = append(calls, append([]string{}, args...))
		line := strings.Join(args, " ")
		if args[0] == "launchctl" {
			return errors.New("no legacy job")
		}
		if strings.Contains(line, "ps --all --format json") {
			out.Write(rows)
		}
		if strings.Contains(line, "config --format json") {
			io.WriteString(out, `{"services":{"api":{"ports":[{"published":"`+port+`"}]}}}`)
		}
		return nil
	}}
	if err := m.start(context.Background(), false, false, 12); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, args := range calls {
		if strings.Contains(strings.Join(args, " "), "up --detach --wait --wait-timeout 12") {
			found = true
			if args[2] != "--project-name" || args[3] != "campustrace" {
				t.Fatal("volume project changed", args)
			}
			if args[5] != filepath.Join(root, "docker-compose.yml") {
				t.Fatal("path split", args)
			}
		}
	}
	if !found {
		t.Fatal("not detached")
	}
}
func TestRebuildQuiescesWritersOnlyAfterBackupAndSuccessfulBuild(t *testing.T) {
	for _, buildFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "build failure"}[buildFails], func(t *testing.T) {
			var operations []string
			rows, _ := json.Marshal(healthy())
			port := unusedPort(t)
			m := manager{root: t.TempDir(), out: io.Discard, errOut: io.Discard, execute: func(_ context.Context, _ string, args []string, out, _ io.Writer) error {
				line := strings.Join(args, " ")
				switch {
				case args[0] == "launchctl":
					return errors.New("no legacy job")
				case strings.Contains(line, "ps --all --format json"):
					out.Write(rows)
				case strings.Contains(line, "config --format json"):
					io.WriteString(out, `{"services":{"api":{"ports":[{"published":"`+port+`"}]}}}`)
				case strings.Contains(line, "mysqldump"):
					operations = append(operations, "backup")
					io.WriteString(out, "synthetic dump")
				case strings.Contains(line, "build api"):
					operations = append(operations, "build")
					if buildFails {
						return errors.New("build failed")
					}
				case strings.Contains(line, " stop "):
					operations = append(operations, "stop")
				case strings.Contains(line, "up --detach"):
					operations = append(operations, "up")
				}
				return nil
			}}
			err := m.start(context.Background(), true, false, 12)
			if (err != nil) != buildFails {
				t.Fatal(err)
			}
			want := []string{"backup", "build", "stop", "up"}
			if buildFails {
				want = want[:2]
			}
			if !reflect.DeepEqual(operations, want) {
				t.Fatal("update could stop healthy services before build or migrate with old writers", operations)
			}
		})
	}
}
func TestPortConflictNeverBuildsOrStopsAnotherService(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	calls := []string{}
	m := manager{root: t.TempDir(), out: io.Discard, errOut: io.Discard, execute: func(_ context.Context, _ string, args []string, out, _ io.Writer) error {
		line := strings.Join(args, " ")
		calls = append(calls, line)
		if args[0] == "launchctl" {
			return errors.New("no job")
		}
		if strings.Contains(line, "config --format json") {
			io.WriteString(out, `{"services":{"api":{"ports":[{"published":"`+port+`"}]}}}`)
		}
		return nil
	}}
	if err := m.start(context.Background(), false, false, 1); err == nil || !strings.Contains(err.Error(), "占用") {
		t.Fatal(err)
	}
	for _, line := range calls {
		if strings.Contains(line, " build ") || strings.Contains(line, " stop ") || strings.Contains(line, " up ") {
			t.Fatal("touched conflicting service", line)
		}
	}
}
func TestBackupNeverOverwritesAndRemovesFailedPartialDump(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "backup.sql")
	var dumpCalls int
	m := manager{root: root, out: io.Discard, errOut: io.Discard, execute: func(_ context.Context, _ string, _ []string, out, _ io.Writer) error {
		dumpCalls++
		io.WriteString(out, "synthetic database dump")
		return nil
	}}
	if _, err := m.backup(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup exposed", info.Mode())
	}
	if _, err := m.backup(context.Background(), destination); err == nil || dumpCalls != 1 {
		t.Fatal("overwrote backup")
	}
	m.execute = func(_ context.Context, _ string, _ []string, out, _ io.Writer) error {
		io.WriteString(out, "partial")
		return errors.New("dump failure")
	}
	failed := filepath.Join(root, "failed.sql")
	if _, err := m.backup(context.Background(), failed); err == nil {
		t.Fatal("accepted failed backup")
	}
	if _, err := os.Stat(failed); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("left partial backup", err)
	}
}
func TestStopRetainsDependenciesAndVolumes(t *testing.T) {
	for _, all := range []bool{false, true} {
		var stopped []string
		m := manager{root: t.TempDir(), out: io.Discard, errOut: io.Discard, execute: func(_ context.Context, _ string, args []string, _, _ io.Writer) error {
			if args[0] == "launchctl" {
				return errors.New("no job")
			}
			for i, v := range args {
				if v == "stop" {
					stopped = args[i+1:]
				}
				if v == "down" || v == "--volumes" {
					t.Fatal("removed data")
				}
			}
			return nil
		}}
		if err := m.stop(context.Background(), all); err != nil {
			t.Fatal(err)
		}
		want := appServices
		if all {
			want = allServices
		}
		if !reflect.DeepEqual(want, stopped) {
			t.Fatal(stopped)
		}
	}
}
func TestHelpAndBadArgumentsDoNotRequireDocker(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), nil, &out, io.Discard); err != nil || !strings.Contains(out.String(), "start") {
		t.Fatal(err, out.String())
	}
	for _, args := range [][]string{{"unknown"}, {"start", "--timeout", "0"}, {"logs", "--tail", "-1"}, {"stop", "extra"}} {
		if Run(context.Background(), args, io.Discard, io.Discard) == nil {
			t.Fatal("accepted", args)
		}
	}
}
func TestProjectDirectoryAndProbe(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"docker-compose.yml", "compose.app.yml"} {
		if err := os.WriteFile(filepath.Join(root, file), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := project(root); err != nil || got != root {
		t.Fatal(got, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	if err := probe(context.Background(), server.URL+"/ready"); err != nil {
		t.Fatal(err)
	}
	if probe(context.Background(), server.URL+"/failed") == nil {
		t.Fatal("accepted unhealthy service")
	}
	if probe(context.Background(), "https://example.com/ready") == nil {
		t.Fatal("allowed external probe")
	}
}

func TestConcurrentLifecycleCommandsAreLockedAndRelease(t *testing.T) {
	root := t.TempDir()
	release, err := acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := acquire(root); err == nil {
		second()
		release()
		t.Fatal("allowed concurrent lifecycle commands")
	}
	release()
	second, err := acquire(root)
	if err != nil {
		t.Fatal("lock did not release", err)
	}
	second()
}
func TestLogFlagsCanFollowTheComponent(t *testing.T) {
	args := logArgs([]string{"api", "--follow", "--tail", "30", "--dir", "project with spaces"})
	want := []string{"--follow", "--tail", "30", "--dir", "project with spaces", "api"}
	if !reflect.DeepEqual(args, want) {
		t.Fatal(args)
	}
}
