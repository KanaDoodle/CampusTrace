package source

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
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
	server, client := net.Pipe()
	defer server.Close()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		calls = append(calls, address)
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("address attempt has no timeout")
		}
		if len(calls) == 1 {
			return nil, errors.New("unreachable first address")
		}
		return client, nil
	}
	conn, err := dialPublicSource(context.Background(), "tcp", "jobs.example.com:443", lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if len(calls) != 2 || calls[0] != "[2606:4700:4700::1111]:443" || calls[1] != "1.1.1.1:443" {
		t.Fatalf("addresses were not pinned or retried: %v", calls)
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
