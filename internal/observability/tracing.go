package observability

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Setup is opt-in. OTLP export is asynchronous and bounded; an unavailable
// observer never becomes an application readiness or admission dependency.
// No baggage, request payloads, provider URLs, credentials or SQL text are sent.
func Setup(ctx context.Context, service string) (func(context.Context) error, error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if endpoint == "" || os.Getenv("OTEL_TRACES_EXPORTER") == "none" {
		return func(context.Context) error { return nil }, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT must be an HTTP(S) URL without credentials or query")
	}
	rate := .1
	if raw := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); raw != "" {
		rate, err = strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(rate) || rate < 0 || rate > 1 {
			return nil, errors.New("OTEL_TRACES_SAMPLER_ARG must be between 0 and 1")
		}
	}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint), otlptracehttp.WithTimeout(2*time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
	if err != nil {
		return nil, errors.New("trace exporter initialization failed")
	}
	attrs := []attribute.KeyValue{attribute.String("service.name", service)}
	if instance := os.Getenv("INSTANCE_ID"); instance != "" && len(instance) <= 80 {
		attrs = append(attrs, attribute.String("service.instance.id", instance))
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(attrs...)),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))),
		sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(2048), sdktrace.WithMaxExportBatchSize(256), sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(2*time.Second)),
		sdktrace.WithSpanLimits(sdktrace.SpanLimits{AttributeValueLengthLimit: 160, AttributeCountLimit: 24, EventCountLimit: 16, LinkCountLimit: 8, AttributePerEventCountLimit: 8, AttributePerLinkCountLimit: 4}),
	)
	var last atomic.Int64
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		now := time.Now().Unix()
		old := last.Load()
		if now-old >= 30 && last.CompareAndSwap(old, now) {
			slog.Warn("trace export unavailable; application continues")
		}
	}))
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

var traceContext = propagation.TraceContext{}

// Only the fixed-size W3C traceparent travels with durable work. Tracestate and
// baggage are deliberately excluded from persistence and model HTTP requests.
func TraceParent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	traceContext.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}
func Extract(ctx context.Context, parent string) context.Context {
	if len(parent) != 55 {
		return ctx
	}
	return traceContext.Extract(ctx, propagation.MapCarrier{"traceparent": parent})
}
func ValidTraceParent(parent string) bool {
	return parent == "" || len(parent) == 55 && trace.SpanContextFromContext(Extract(context.Background(), parent)).IsValid()
}
func Start(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer("campustrace").Start(ctx, name)
}
func Producer(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer("campustrace").Start(ctx, name, trace.WithSpanKind(trace.SpanKindProducer))
}
func Consumer(ctx context.Context, name, parent string) (context.Context, trace.Span) {
	return otel.Tracer("campustrace").Start(Extract(ctx, parent), name, trace.WithSpanKind(trace.SpanKindConsumer))
}
func Client(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer("campustrace").Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient))
}
func Server(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer("campustrace").Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
}
func HTTPContext(ctx context.Context, headers map[string][]string) context.Context {
	// Read only traceparent; never import request baggage or vendor tracestate.
	return Extract(ctx, propagation.HeaderCarrier(headers).Get("traceparent"))
}
func End(span trace.Span, err error) {
	if err != nil {
		reason := "operation failed"
		if errors.Is(err, context.Canceled) {
			reason = "cancelled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			reason = "deadline exceeded"
		}
		span.SetStatus(codes.Error, reason)
		span.RecordError(errors.New(reason))
	}
	span.End()
}

// ContextHandler joins existing structured logs to traces without adding user
// documents or model input. Attributes and groups retain slog's usual behavior.
type ContextHandler struct{ slog.Handler }

func (h ContextHandler) Handle(ctx context.Context, record slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		record.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, record)
}
func (h ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ContextHandler{h.Handler.WithAttrs(attrs)}
}
func (h ContextHandler) WithGroup(name string) slog.Handler {
	return ContextHandler{h.Handler.WithGroup(name)}
}
