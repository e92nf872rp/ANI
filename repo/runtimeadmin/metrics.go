package runtimeadmin

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel/attribute"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

func newMetrics(identity Identity, collectors []prometheus.Collector) (*sdkmetric.MeterProvider, http.Handler, error) {
	registry := prometheus.NewRegistry()
	for index, collector := range collectors {
		if collector == nil {
			return nil, nil, fmt.Errorf("collector %d is nil", index)
		}
		if err := registry.Register(collector); err != nil {
			return nil, nil, fmt.Errorf("register collector %d: %w", index, err)
		}
	}

	exporter, err := otelprometheus.New(
		otelprometheus.WithRegisterer(registry),
		otelprometheus.WithTranslationStrategy(otlptranslator.UnderscoreEscapingWithSuffixes),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create Prometheus exporter: %w", err)
	}

	attributes := []attribute.KeyValue{
		attribute.String("service.namespace", identity.Namespace),
		attribute.String("service.name", identity.Name),
		attribute.String("service.instance.id", identity.InstanceID),
	}
	if identity.Version != "" {
		attributes = append(attributes, attribute.String("service.version", identity.Version))
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resource.NewSchemaless(attributes...)),
		sdkmetric.WithReader(exporter),
	)
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{ErrorHandling: promhttp.HTTPErrorOnError})
	return meterProvider, handler, nil
}
