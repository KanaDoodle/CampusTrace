package observability

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestOTLPPropagationRedactionAndContextLogs(t *testing.T) {
	requests := make(chan *collector.ExportTraceServiceRequest, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			z, e := gzip.NewReader(r.Body)
			if e != nil {
				http.Error(w, "gzip", 400)
				return
			}
			defer z.Close()
			reader = z
		}
		raw, e := io.ReadAll(reader)
		v := new(collector.ExportTraceServiceRequest)
		if e != nil || proto.Unmarshal(raw, v) != nil {
			http.Error(w, "protobuf", 400)
			return
		}
		requests <- v
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}))
	defer server.Close()
	previous := otel.GetTracerProvider()
	defer otel.SetTracerProvider(previous)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", server.URL+"/v1/traces")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1")
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	stop, e := Setup(context.Background(), "test-observer")
	if e != nil {
		t.Fatal(e)
	}
	defer stop(context.Background())
	root, parent := Server(context.Background(), "HTTP synthetic")
	member, _ := baggage.NewMember("private", "never-export-this-email@example.invalid")
	bag, _ := baggage.New(member)
	root = baggage.ContextWithBaggage(root, bag)
	encoded := TraceParent(root)
	if !ValidTraceParent(encoded) || len(encoded) != 55 {
		t.Fatal(encoded)
	}
	remote := HTTPContext(context.Background(), http.Header{"Traceparent": []string{encoded}, "Tracestate": []string{"private=secret"}, "Baggage": []string{"private=secret"}})
	if baggage.FromContext(remote).Len() != 0 || trace.SpanContextFromContext(remote).TraceState().Len() != 0 {
		t.Fatal("untrusted baggage propagated")
	}
	childCtx, child := Consumer(remote, "task.process", encoded)
	if child.SpanContext().TraceID() != parent.SpanContext().TraceID() {
		t.Fatal("trace detached")
	}
	var logs bytes.Buffer
	slog.New(ContextHandler{Handler: slog.NewJSONHandler(&logs, nil)}).InfoContext(childCtx, "synthetic operation")
	if !strings.Contains(logs.String(), child.SpanContext().TraceID().String()) || strings.Contains(logs.String(), "never-export") {
		t.Fatal(logs.String())
	}
	End(child, errors.New("https://secret-host.invalid/?key=never-export-secret"))
	parent.End()
	flush, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(flush); e != nil {
		t.Fatal(e)
	}
	select {
	case request := <-requests:
		raw, _ := protojson.Marshal(request)
		for _, secret := range []string{"never-export", "secret-host", "private=secret"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("trace leaked private content")
			}
		}
		var rootID, childParent []byte
		count := 0
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					count++
					if span.Name == "HTTP synthetic" {
						rootID = span.SpanId
					}
					if span.Name == "task.process" {
						childParent = span.ParentSpanId
					}
				}
			}
		}
		if count != 2 || !bytes.Equal(rootID, childParent) {
			t.Fatal("missing parent-child relationship", count)
		}
	case <-flush.Done():
		t.Fatal("OTLP exporter delivered no spans")
	}
}

func TestObserverFailureIsAsynchronousAndConfigurationIsValidated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	previous := otel.GetTracerProvider()
	defer otel.SetTracerProvider(previous)
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", server.URL+"/v1/traces")
	stop, e := Setup(context.Background(), "failure-observer")
	if e != nil {
		t.Fatal(e)
	}
	defer stop(context.Background())
	started := time.Now()
	for i := 0; i < 10; i++ {
		_, span := Start(context.Background(), "business continues")
		span.End()
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatal("observer blocked business")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(ctx); e == nil {
		t.Fatal("fixture collector failure was not exercised")
	}
	for _, rate := range []string{"NaN", "-1", "1.1"} {
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", rate)
		if _, e = Setup(context.Background(), "invalid"); e == nil {
			t.Fatal("accepted sampler", rate)
		}
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://user:secret@localhost/v1/traces")
	if _, e = Setup(context.Background(), "invalid"); e == nil {
		t.Fatal("accepted credentials in trace URL")
	}
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	if _, e = Setup(context.Background(), "disabled"); e != nil {
		t.Fatal("opt-out ignored", e)
	}
}
