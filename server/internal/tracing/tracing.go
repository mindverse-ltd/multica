package tracing

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const shutdownTimeout = 5 * time.Second

var (
	mu       sync.Mutex
	provider *sdktrace.TracerProvider
)

// Init configures OpenTelemetry tracing from standard OTEL_* environment
// variables. It is a no-op unless an OTLP trace endpoint is configured.
func Init(ctx context.Context) (func(context.Context) error, error) {
	if sdkDisabled() || !endpointConfigured() {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	mu.Lock()
	provider = tp
	mu.Unlock()

	return Shutdown, nil
}

// Shutdown flushes and closes the configured tracer provider.
func Shutdown(ctx context.Context) error {
	mu.Lock()
	tp := provider
	provider = nil
	mu.Unlock()
	if tp == nil {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	var errs []error
	if err := tp.ForceFlush(shutdownCtx); err != nil {
		errs = append(errs, err)
	}
	if err := tp.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, err)
	}
	otel.SetTracerProvider(noop.NewTracerProvider())
	if err := errors.Join(errs...); err != nil {
		slog.Warn("opentelemetry shutdown failed", "error", err)
		return err
	}
	return nil
}

func endpointConfigured() bool {
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
}

func sdkDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true")
}
