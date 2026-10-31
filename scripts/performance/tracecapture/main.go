// A bounded local OTLP/HTTP receiver for synthetic diagnostic runs. It retains
// only span identity/timing/status and service names, never payload attributes.
package main

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func main() {
	address := flag.String("addr", "127.0.0.1:4318", "private diagnostic listener")
	output := flag.String("output", "traces.jsonl", "new output file")
	flag.Parse()
	f, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var reader io.Reader = http.MaxBytesReader(w, r.Body, 8<<20)
		if r.Header.Get("Content-Encoding") == "gzip" {
			z, e := gzip.NewReader(reader)
			if e != nil {
				w.WriteHeader(400)
				return
			}
			defer z.Close()
			reader = z
		}
		raw, e := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
		var request collector.ExportTraceServiceRequest
		if e != nil || len(raw) > 8<<20 || proto.Unmarshal(raw, &request) != nil {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		encoder := json.NewEncoder(f)
		for _, resource := range request.ResourceSpans {
			service, instance := "", ""
			for _, attr := range resource.GetResource().GetAttributes() {
				if attr.Key == "service.name" {
					service = attr.Value.GetStringValue()
				}
				if attr.Key == "service.instance.id" {
					instance = attr.Value.GetStringValue()
				}
			}
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					e = encoder.Encode(map[string]any{"service": service, "instance": instance, "name": span.Name,
						"trace_id": hex.EncodeToString(span.TraceId), "span_id": hex.EncodeToString(span.SpanId),
						"parent_id": hex.EncodeToString(span.ParentSpanId), "start_ns": span.StartTimeUnixNano,
						"end_ns": span.EndTimeUnixNano, "duration_ms": float64(span.EndTimeUnixNano-span.StartTimeUnixNano) / 1e6,
						"status": span.GetStatus().GetCode().String()})
					if e != nil {
						w.WriteHeader(500)
						return
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	})
	log.Fatal((&http.Server{Addr: *address, Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}).ListenAndServe())
}
