package source

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestImportAndSSRF(t *testing.T) {
	rows, err := CSV(strings.NewReader("company,title,job_type,locations,source_id,text\nExample,Backend,FULL_TIME,Shanghai,manual,hello\n"))
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	if _, err = JSON(strings.NewReader(`[{"company":"x","bad":1}]`)); err == nil {
		t.Fatal("unknown accepted")
	}
	r, err := (HTTPAdapter{Client: PublicClient()}).Fetch(context.Background(), "http://127.0.0.1:12379")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "HTTP_ERROR" {
		t.Fatal("private endpoint fetched", r)
	}
}

func TestPublicSourceDialFallsBackToOtherValidatedAddresses(t *testing.T) {
	ips := []net.IPAddr{{IP: net.ParseIP("2606:4700:4700::1111")}, {IP: net.ParseIP("1.1.1.1")}}
	lookup := func(context.Context, string) ([]net.IPAddr, error) { return ips, nil }
	calls := []string{}
	var mu sync.Mutex
	failed := make(chan struct{})
	server, client := net.Pipe()
	defer server.Close()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		calls = append(calls, address)
		mu.Unlock()
		if _, ok := ctx.Deadline(); !ok {
			t.Error("address attempt has no timeout")
		}
		if address == "[2606:4700:4700::1111]:443" {
			close(failed)
			return nil, errors.New("unreachable first address")
		}
		<-failed
		return client, nil
	}
	conn, err := dialPublicSource(context.Background(), "tcp", "jobs.example.com:443", lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || !(calls[0] == "[2606:4700:4700::1111]:443" && calls[1] == "1.1.1.1:443" || calls[1] == "[2606:4700:4700::1111]:443" && calls[0] == "1.1.1.1:443") {
		t.Fatalf("addresses were not pinned or retried: %v", calls)
	}
}

func TestPublicDialSlowAddressDoesNotBlockAndLosingConnectionCloses(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	started := make(chan struct{})
	slowServer, slowClient := net.Pipe()
	defer slowServer.Close()
	fastServer, fastClient := net.Pipe()
	defer fastServer.Close()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "1.1.1.1:443" {
			close(started)
			<-ctx.Done()
			// A dial may finish concurrently with cancellation. Its connection
			// still needs to close after another attempt has won.
			return slowClient, nil
		}
		<-started
		return fastClient, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dialPublicSource(ctx, "tcp", "jobs.example.com:443", lookup, dial)
	if err != nil || conn != fastClient {
		t.Fatalf("slow address blocked fallback: %v", err)
	}
	defer conn.Close()
	_ = slowServer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := slowServer.Read(make([]byte, 1)); err == nil {
		t.Fatal("losing connection not closed")
	} else if v, ok := err.(net.Error); ok && v.Timeout() {
		t.Fatal("losing connection leaked")
	}
}

func TestPublicSourceDialChecksAllAddressesBeforeAnyConnection(t *testing.T) {
	for _, private := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "::1"} {
		lookup := func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP(private)}}, nil
		}
		dial := func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("connected before validating the entire DNS response")
			return nil, nil
		}
		if _, err := dialPublicSource(context.Background(), "tcp", "jobs.example.com:443", lookup, dial); err == nil {
			t.Fatalf("accepted private DNS address %s", private)
		}
	}
}

func TestPublicDialLimitsAttemptsAndPropagatesFailures(t *testing.T) {
	ips := []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("9.9.9.9")}, {IP: net.ParseIP("208.67.222.222")}}
	lookup := func(context.Context, string) ([]net.IPAddr, error) { return ips, nil }
	var mu sync.Mutex
	active, peak, calls := 0, 0, 0
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		active++
		calls++
		peak = max(peak, active)
		mu.Unlock()
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
		mu.Lock()
		active--
		mu.Unlock()
		return nil, errors.New("public address unreachable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn, err := dialPublicSource(ctx, "tcp", "jobs.example.com:443", lookup, dial); err == nil || conn != nil {
		t.Fatal("failed connections accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 2 || calls != len(ips) || active != 0 {
		t.Fatalf("unbounded or unfinished attempts: peak=%d calls=%d active=%d", peak, calls, active)
	}
}
