"""Optional OpenTelemetry instrumentation for TokenWatch."""

import logging

from .config import OTEL_ENABLED, OTEL_ENDPOINT, OTEL_SERVICE_NAME

logger = logging.getLogger("tokenwatch")

# Lazy-initialized globals
_tracer = None
_meter = None
_request_counter = None
_token_counter_in = None
_token_counter_out = None
_cost_counter = None
_latency_histogram = None
_cache_hit_counter = None


def init_telemetry():
    """Initialize OTEL tracer and meters. No-op if disabled."""
    if not OTEL_ENABLED:
        return

    global _tracer, _meter
    global _request_counter, _token_counter_in, _token_counter_out
    global _cost_counter, _latency_histogram, _cache_hit_counter

    try:
        from opentelemetry import trace, metrics
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.metrics import MeterProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor
        from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
        from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
        from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
        from opentelemetry.sdk.resources import Resource

        resource = Resource.create({"service.name": OTEL_SERVICE_NAME})

        # Traces
        tp = TracerProvider(resource=resource)
        tp.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=OTEL_ENDPOINT)))
        trace.set_tracer_provider(tp)
        _tracer = trace.get_tracer("tokenwatch")

        # Metrics
        reader = PeriodicExportingMetricReader(OTLPMetricExporter(endpoint=OTEL_ENDPOINT))
        mp = MeterProvider(resource=resource, metric_readers=[reader])
        metrics.set_meter_provider(mp)
        _meter = metrics.get_meter("tokenwatch")

        _request_counter = _meter.create_counter("tokenwatch.requests.total")
        _token_counter_in = _meter.create_counter("tokenwatch.tokens.input")
        _token_counter_out = _meter.create_counter("tokenwatch.tokens.output")
        _cost_counter = _meter.create_counter("tokenwatch.cost.total")
        _latency_histogram = _meter.create_histogram("tokenwatch.latency.upstream")
        _cache_hit_counter = _meter.create_counter("tokenwatch.cache.hits")

        logger.info("OpenTelemetry initialized: endpoint=%s", OTEL_ENDPOINT)
    except ImportError:
        logger.warning("OpenTelemetry packages not installed. Install with: pip install tokenwatch[otel]")
    except Exception:
        logger.exception("Failed to initialize OpenTelemetry")


def get_tracer():
    return _tracer


def record_request_metrics(record):
    """Record metrics for a completed request."""
    if not _request_counter:
        return

    labels = {
        "model": record.model,
        "api_type": record.api_type,
        "source_app": record.source_app,
        "cache_hit": str(record.cache_hit),
    }
    _request_counter.add(1, labels)
    _token_counter_in.add(record.input_tokens, {"model": record.model})
    _token_counter_out.add(record.output_tokens, {"model": record.model})
    if record.estimated_cost:
        _cost_counter.add(record.estimated_cost, {"model": record.model, "feature_tag": record.feature_tag})
    _latency_histogram.record(record.latency_ms, {"model": record.model})
    if record.cache_hit:
        _cache_hit_counter.add(1, {"model": record.model})
