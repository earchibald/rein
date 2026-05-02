package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	otelslog "go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const (
	DefaultServiceName   = "rein-daemon"
	defaultMetricsPeriod = 30 * time.Second
)

type Config struct {
	Endpoint           string
	Insecure           bool
	Headers            map[string]string
	ServiceName        string
	ResourceAttributes map[string]string
	MetricsPeriod      time.Duration
}

func (c Config) Enabled() bool {
	return strings.TrimSpace(c.Endpoint) != ""
}

func (c Config) Normalize() (Config, error) {
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	if c.Endpoint == "" {
		return c, nil
	}

	if parsed, err := url.Parse(c.Endpoint); err == nil && parsed.Host != "" {
		switch parsed.Scheme {
		case "http":
			c.Endpoint = parsed.Host
			c.Insecure = true
		case "https":
			c.Endpoint = parsed.Host
		case "":
		default:
			return Config{}, fmt.Errorf("unsupported OTLP endpoint scheme %q", parsed.Scheme)
		}
	}

	if c.ServiceName == "" {
		c.ServiceName = DefaultServiceName
	}
	if c.MetricsPeriod <= 0 {
		c.MetricsPeriod = defaultMetricsPeriod
	}
	c.Headers = cloneMap(c.Headers)
	c.ResourceAttributes = cloneMap(c.ResourceAttributes)
	return c, nil
}

func ParseKeyValueList(raw string) (map[string]string, error) {
	items := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid key=value pair %q", part)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("invalid key=value pair %q", part)
		}
		items[key] = value
	}
	return items, nil
}

type Runtime struct {
	Logger      *slog.Logger
	GRPCOptions []grpc.ServerOption

	metrics   *daemonMetrics
	running   atomic.Bool
	shutdowns []func(context.Context) error
	tracer    trace.Tracer
}

func NewDaemonRuntime(ctx context.Context, config Config, stdout io.Writer) (*Runtime, error) {
	if stdout == nil {
		return nil, errors.New("telemetry stdout writer is required")
	}

	textHandler := slog.NewTextHandler(stdout, nil)
	config, err := config.Normalize()
	if err != nil {
		return nil, err
	}

	runtime := &Runtime{
		Logger: slog.New(textHandler),
		tracer: otel.Tracer("github.com/earchibald/rein/internal/telemetry"),
	}
	if !config.Enabled() {
		return runtime, nil
	}

	res, err := buildResource(ctx, config)
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracegrpc.New(ctx, traceExporterOptions(config)...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	metricExporter, err := otlpmetricgrpc.New(ctx, metricExporterOptions(config)...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}
	logExporter, err := otlploggrpc.New(ctx, logExporterOptions(config)...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP log exporter: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(config.MetricsPeriod))),
		sdkmetric.WithResource(res),
	)
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)

	otelHandler := otelslog.NewHandler("github.com/earchibald/rein/internal/telemetry", otelslog.WithLoggerProvider(loggerProvider))
	runtime.Logger = slog.New(newFanoutHandler(textHandler, otelHandler))
	runtime.tracer = tracerProvider.Tracer("github.com/earchibald/rein/internal/telemetry")

	runtime.running.Store(true)
	metrics, err := newDaemonMetrics(meterProvider.Meter("github.com/earchibald/rein/internal/telemetry"), &runtime.running)
	if err != nil {
		return nil, err
	}
	runtime.metrics = metrics
	runtime.GRPCOptions = []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(runtime.unaryServerInterceptor()),
		grpc.ChainStreamInterceptor(runtime.streamServerInterceptor()),
	}
	runtime.shutdowns = []func(context.Context) error{
		func(context.Context) error {
			if runtime.metrics != nil {
				return runtime.metrics.Close()
			}
			return nil
		},
		loggerProvider.Shutdown,
		meterProvider.Shutdown,
		tracerProvider.Shutdown,
	}
	return runtime, nil
}

func (r *Runtime) RecordStartup(ctx context.Context) {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.starts.Add(ctx, 1)
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.running.Store(false)
	var errs []error
	for _, shutdown := range r.shutdowns {
		if shutdown == nil {
			continue
		}
		if err := shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Runtime) unaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, span := r.startSpan(ctx, info.FullMethod, false)
		startedAt := time.Now()
		resp, err := handler(ctx, req)
		r.finishSpan(span, info.FullMethod, false, startedAt, err)
		return resp, err
	}
}

func (r *Runtime) streamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, span := r.startSpan(stream.Context(), info.FullMethod, true)
		startedAt := time.Now()
		err := handler(srv, &telemetryServerStream{ServerStream: stream, ctx: ctx})
		r.finishSpan(span, info.FullMethod, true, startedAt, err)
		return err
	}
}

func (r *Runtime) startSpan(ctx context.Context, fullMethod string, stream bool) (context.Context, trace.Span) {
	if r == nil || r.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return r.tracer.Start(ctx, fullMethod, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(rpcAttributes(fullMethod, stream)...))
}

func (r *Runtime) finishSpan(span trace.Span, fullMethod string, stream bool, startedAt time.Time, err error) {
	code := grpcstatus.Code(err)
	attrs := append(rpcAttributes(fullMethod, stream), attribute.Int("rpc.grpc.status_code", int(code)), attribute.String("rpc.grpc.status_text", code.String()))
	if span != nil {
		span.SetAttributes(attrs...)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, code.String())
		} else {
			span.SetStatus(otelcodes.Ok, code.String())
		}
		span.End()
	}
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.RecordRPC(context.Background(), startedAt, code, attrs...)
}

func rpcAttributes(fullMethod string, stream bool) []attribute.KeyValue {
	method := strings.TrimPrefix(strings.TrimSpace(fullMethod), "/")
	serviceName := method
	methodName := method
	if prefix, suffix, ok := strings.Cut(method, "/"); ok {
		serviceName = prefix
		methodName = suffix
	}
	return []attribute.KeyValue{
		attribute.String("rpc.system", "grpc"),
		attribute.String("rpc.service", serviceName),
		attribute.String("rpc.method", methodName),
		attribute.Bool("rpc.stream", stream),
	}
}

func buildResource(ctx context.Context, config Config) (*resource.Resource, error) {
	keys := make([]string, 0, len(config.ResourceAttributes)+1)
	keys = append(keys, "service.name")
	for key := range config.ResourceAttributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	attrs := make([]attribute.KeyValue, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		value := config.ResourceAttributes[key]
		if key == "service.name" {
			value = config.ServiceName
		}
		if value == "" {
			continue
		}
		attrs = append(attrs, attribute.String(key, value))
	}
	return resource.New(ctx, resource.WithAttributes(attrs...))
}

func traceExporterOptions(config Config) []otlptracegrpc.Option {
	options := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(config.Endpoint)}
	if config.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	}
	if len(config.Headers) > 0 {
		options = append(options, otlptracegrpc.WithHeaders(config.Headers))
	}
	return options
}

func metricExporterOptions(config Config) []otlpmetricgrpc.Option {
	options := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(config.Endpoint)}
	if config.Insecure {
		options = append(options, otlpmetricgrpc.WithInsecure())
	}
	if len(config.Headers) > 0 {
		options = append(options, otlpmetricgrpc.WithHeaders(config.Headers))
	}
	return options
}

func logExporterOptions(config Config) []otlploggrpc.Option {
	options := []otlploggrpc.Option{otlploggrpc.WithEndpoint(config.Endpoint)}
	if config.Insecure {
		options = append(options, otlploggrpc.WithInsecure())
	}
	if len(config.Headers) > 0 {
		options = append(options, otlploggrpc.WithHeaders(config.Headers))
	}
	return options
}

type daemonMetrics struct {
	starts       metric.Int64Counter
	requests     metric.Int64Counter
	errors       metric.Int64Counter
	duration     metric.Float64Histogram
	registration metric.Registration
}

func newDaemonMetrics(meter metric.Meter, running *atomic.Bool) (*daemonMetrics, error) {
	starts, err := meter.Int64Counter("rein.daemon.starts", metric.WithDescription("Count of daemon starts."))
	if err != nil {
		return nil, fmt.Errorf("create rein.daemon.starts counter: %w", err)
	}
	requests, err := meter.Int64Counter("rein.rpc.requests", metric.WithDescription("Count of gRPC requests handled by the daemon."))
	if err != nil {
		return nil, fmt.Errorf("create rein.rpc.requests counter: %w", err)
	}
	errorsCounter, err := meter.Int64Counter("rein.rpc.errors", metric.WithDescription("Count of gRPC requests that returned a non-OK status."))
	if err != nil {
		return nil, fmt.Errorf("create rein.rpc.errors counter: %w", err)
	}
	duration, err := meter.Float64Histogram("rein.rpc.duration", metric.WithDescription("gRPC request duration in milliseconds."), metric.WithUnit("ms"))
	if err != nil {
		return nil, fmt.Errorf("create rein.rpc.duration histogram: %w", err)
	}
	runningGauge, err := meter.Int64ObservableGauge("rein.daemon.running", metric.WithDescription("Whether the daemon process is running."))
	if err != nil {
		return nil, fmt.Errorf("create rein.daemon.running gauge: %w", err)
	}
	registration, err := meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		value := int64(0)
		if running != nil && running.Load() {
			value = 1
		}
		observer.ObserveInt64(runningGauge, value)
		return nil
	}, runningGauge)
	if err != nil {
		return nil, fmt.Errorf("register daemon.running callback: %w", err)
	}
	return &daemonMetrics{
		starts:       starts,
		requests:     requests,
		errors:       errorsCounter,
		duration:     duration,
		registration: registration,
	}, nil
}

func (m *daemonMetrics) RecordRPC(ctx context.Context, startedAt time.Time, code grpccodes.Code, attrs ...attribute.KeyValue) {
	if m == nil {
		return
	}
	options := metric.WithAttributes(attrs...)
	m.requests.Add(ctx, 1, options)
	m.duration.Record(ctx, float64(time.Since(startedAt))/float64(time.Millisecond), options)
	if code != grpccodes.OK {
		m.errors.Add(ctx, 1, options)
	}
}

func (m *daemonMetrics) Close() error {
	if m == nil || m.registration == nil {
		return nil
	}
	return m.registration.Unregister()
}

type telemetryServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *telemetryServerStream) Context() context.Context {
	return s.ctx
}

type fanoutHandler struct {
	handlers []slog.Handler
}

func newFanoutHandler(handlers ...slog.Handler) slog.Handler {
	filtered := make([]slog.Handler, 0, len(handlers))
	for _, handler := range handlers {
		if handler != nil {
			filtered = append(filtered, handler)
		}
	}
	return fanoutHandler{handlers: filtered}
}

func (h fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	for _, handler := range h.handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}
	return fanoutHandler{handlers: handlers}
}

func (h fanoutHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return fanoutHandler{handlers: handlers}
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
