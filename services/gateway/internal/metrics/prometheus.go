package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// PrometheusExporter exports metrics in Prometheus format
type PrometheusExporter struct {
	// Request metrics
	requestsTotal       atomic.Int64
	requestsSuccess     atomic.Int64
	requestsFailed      atomic.Int64
	requestsDuration    *HistogramCollector
	
	// Throughput metrics
	eventsIngested      atomic.Int64
	eventsProcessed     atomic.Int64
	bytesIngested       atomic.Int64
	bytesProcessed      atomic.Int64
	
	// Kafka metrics
	kafkaProducerErrors atomic.Int64
	kafkaProducerSent   atomic.Int64
	kafkaLag            atomic.Int64
	kafkaOffsets        sync.Map // map[string]int64 partition->offset
	
	// Watermark metrics
	watermarkLag        atomic.Int64
	watermarkMin        atomic.Int64
	watermarkMax        atomic.Int64
	dataFreshness       *HistogramCollector
	
	// System metrics
	cpuUsage            atomic.Uint64 // Stored as percentage * 100
	memoryUsage         atomic.Uint64 // Stored as percentage * 100
	goroutines          atomic.Int32
	openConnections     atomic.Int64
	
	// Backpressure metrics
	queueDepth          atomic.Int64
	queueCapacity       atomic.Int64
	shedRequests        atomic.Int64
	circuitBreakerOpen  atomic.Int32
	rateLimitHits       atomic.Int64
	
	// Cache metrics
	cacheHits           atomic.Int64
	cacheMisses         atomic.Int64
	cacheEvictions      atomic.Int64
	cacheSize           atomic.Int64
	
	// Error metrics by type
	errorsByType        sync.Map // map[string]int64
	
	// Labels
	namespace           string
	service             string
	instance            string
	
	// Start time for uptime calculation
	startTime           time.Time
}

// HistogramCollector collects histogram data for percentiles
type HistogramCollector struct {
	mu          sync.RWMutex
	values      []float64
	maxSize     int
	sum         float64
	count       int64
}

// NewPrometheusExporter creates a new Prometheus exporter
func NewPrometheusExporter(namespace, service, instance string) *PrometheusExporter {
	return &PrometheusExporter{
		namespace:         namespace,
		service:          service,
		instance:         instance,
		requestsDuration: NewHistogramCollector(10000),
		dataFreshness:    NewHistogramCollector(10000),
		startTime:        time.Now(),
	}
}

// NewHistogramCollector creates a new histogram collector
func NewHistogramCollector(maxSize int) *HistogramCollector {
	return &HistogramCollector{
		values:  make([]float64, 0, maxSize),
		maxSize: maxSize,
	}
}

// Observe adds a value to the histogram
func (h *HistogramCollector) Observe(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	if len(h.values) >= h.maxSize {
		// Remove oldest value (simple ring buffer)
		h.values = h.values[1:]
	}
	h.values = append(h.values, value)
	h.sum += value
	h.count++
}

// GetPercentile calculates a percentile
func (h *HistogramCollector) GetPercentile(p float64) float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	if len(h.values) == 0 {
		return 0
	}
	
	// Simple percentile calculation (not exact but fast)
	index := int(float64(len(h.values)) * p / 100)
	if index >= len(h.values) {
		index = len(h.values) - 1
	}
	return h.values[index]
}

// ServeHTTP handles Prometheus metrics requests
func (pe *PrometheusExporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	
	// Helper function to write metric
	writeMetric := func(name, help, metricType string, value interface{}, labels ...string) {
		fmt.Fprintf(w, "# HELP %s_%s %s\n", pe.namespace, name, help)
		fmt.Fprintf(w, "# TYPE %s_%s %s\n", pe.namespace, name, metricType)
		
		labelStr := pe.formatLabels(labels...)
		fmt.Fprintf(w, "%s_%s%s %v\n", pe.namespace, name, labelStr, value)
	}
	
	// Request metrics
	writeMetric("requests_total", "Total number of requests", "counter", 
		pe.requestsTotal.Load())
	writeMetric("requests_success_total", "Total successful requests", "counter", 
		pe.requestsSuccess.Load())
	writeMetric("requests_failed_total", "Total failed requests", "counter", 
		pe.requestsFailed.Load())
	
	// Request duration percentiles
	fmt.Fprintf(w, "# HELP %s_request_duration_seconds Request duration in seconds\n", pe.namespace)
	fmt.Fprintf(w, "# TYPE %s_request_duration_seconds histogram\n", pe.namespace)
	labels := pe.formatLabels()
	fmt.Fprintf(w, "%s_request_duration_seconds{quantile=\"0.5\"%s} %f\n", 
		pe.namespace, labels, pe.requestsDuration.GetPercentile(50)/1000)
	fmt.Fprintf(w, "%s_request_duration_seconds{quantile=\"0.9\"%s} %f\n", 
		pe.namespace, labels, pe.requestsDuration.GetPercentile(90)/1000)
	fmt.Fprintf(w, "%s_request_duration_seconds{quantile=\"0.95\"%s} %f\n", 
		pe.namespace, labels, pe.requestsDuration.GetPercentile(95)/1000)
	fmt.Fprintf(w, "%s_request_duration_seconds{quantile=\"0.99\"%s} %f\n", 
		pe.namespace, labels, pe.requestsDuration.GetPercentile(99)/1000)
	fmt.Fprintf(w, "%s_request_duration_seconds_sum%s %f\n", 
		pe.namespace, labels, pe.requestsDuration.sum/1000)
	fmt.Fprintf(w, "%s_request_duration_seconds_count%s %d\n", 
		pe.namespace, labels, pe.requestsDuration.count)
	
	// Throughput metrics
	writeMetric("events_ingested_total", "Total events ingested", "counter", 
		pe.eventsIngested.Load())
	writeMetric("events_processed_total", "Total events processed", "counter", 
		pe.eventsProcessed.Load())
	writeMetric("bytes_ingested_total", "Total bytes ingested", "counter", 
		pe.bytesIngested.Load())
	writeMetric("bytes_processed_total", "Total bytes processed", "counter", 
		pe.bytesProcessed.Load())
	
	// Kafka metrics
	writeMetric("kafka_producer_errors_total", "Kafka producer errors", "counter", 
		pe.kafkaProducerErrors.Load())
	writeMetric("kafka_producer_sent_total", "Messages sent to Kafka", "counter", 
		pe.kafkaProducerSent.Load())
	writeMetric("kafka_consumer_lag", "Kafka consumer lag", "gauge", 
		pe.kafkaLag.Load())
	
	// Watermark metrics
	writeMetric("watermark_lag_ms", "Watermark lag in milliseconds", "gauge", 
		pe.watermarkLag.Load())
	writeMetric("watermark_min", "Minimum watermark timestamp", "gauge", 
		pe.watermarkMin.Load())
	writeMetric("watermark_max", "Maximum watermark timestamp", "gauge", 
		pe.watermarkMax.Load())
	
	// Data freshness percentiles
	fmt.Fprintf(w, "# HELP %s_data_freshness_ms Data freshness in milliseconds\n", pe.namespace)
	fmt.Fprintf(w, "# TYPE %s_data_freshness_ms histogram\n", pe.namespace)
	fmt.Fprintf(w, "%s_data_freshness_ms{quantile=\"0.5\"%s} %f\n", 
		pe.namespace, labels, pe.dataFreshness.GetPercentile(50))
	fmt.Fprintf(w, "%s_data_freshness_ms{quantile=\"0.95\"%s} %f\n", 
		pe.namespace, labels, pe.dataFreshness.GetPercentile(95))
	fmt.Fprintf(w, "%s_data_freshness_ms{quantile=\"0.99\"%s} %f\n", 
		pe.namespace, labels, pe.dataFreshness.GetPercentile(99))
	
	// System metrics
	writeMetric("cpu_usage_percent", "CPU usage percentage", "gauge", 
		float64(pe.cpuUsage.Load())/100)
	writeMetric("memory_usage_percent", "Memory usage percentage", "gauge", 
		float64(pe.memoryUsage.Load())/100)
	writeMetric("goroutines", "Number of goroutines", "gauge", 
		pe.goroutines.Load())
	writeMetric("open_connections", "Number of open connections", "gauge", 
		pe.openConnections.Load())
	
	// Backpressure metrics
	writeMetric("queue_depth", "Current queue depth", "gauge", 
		pe.queueDepth.Load())
	writeMetric("queue_capacity", "Queue capacity", "gauge", 
		pe.queueCapacity.Load())
	writeMetric("shed_requests_total", "Total requests shed", "counter", 
		pe.shedRequests.Load())
	writeMetric("circuit_breaker_open", "Circuit breaker state (1=open)", "gauge", 
		pe.circuitBreakerOpen.Load())
	writeMetric("rate_limit_hits_total", "Rate limit hits", "counter", 
		pe.rateLimitHits.Load())
	
	// Cache metrics
	cacheTotal := pe.cacheHits.Load() + pe.cacheMisses.Load()
	cacheHitRate := float64(0)
	if cacheTotal > 0 {
		cacheHitRate = float64(pe.cacheHits.Load()) / float64(cacheTotal)
	}
	writeMetric("cache_hits_total", "Cache hits", "counter", 
		pe.cacheHits.Load())
	writeMetric("cache_misses_total", "Cache misses", "counter", 
		pe.cacheMisses.Load())
	writeMetric("cache_hit_rate", "Cache hit rate", "gauge", 
		cacheHitRate)
	writeMetric("cache_evictions_total", "Cache evictions", "counter", 
		pe.cacheEvictions.Load())
	writeMetric("cache_size", "Current cache size", "gauge", 
		pe.cacheSize.Load())
	
	// Error metrics by type
	pe.errorsByType.Range(func(key, value interface{}) bool {
		errorType := key.(string)
		count := value.(*atomic.Int64).Load()
		fmt.Fprintf(w, "%s_errors_total{type=\"%s\"%s} %d\n", 
			pe.namespace, errorType, labels, count)
		return true
	})
	
	// Uptime
	uptime := time.Since(pe.startTime).Seconds()
	writeMetric("uptime_seconds", "Uptime in seconds", "gauge", uptime)
}

// formatLabels formats labels for Prometheus
func (pe *PrometheusExporter) formatLabels(additional ...string) string {
	labels := fmt.Sprintf(",service=\"%s\",instance=\"%s\"", pe.service, pe.instance)
	
	for i := 0; i < len(additional); i += 2 {
		if i+1 < len(additional) {
			labels += fmt.Sprintf(",%s=\"%s\"", additional[i], additional[i+1])
		}
	}
	
	if labels != "" {
		return "{" + labels[1:] + "}"
	}
	return ""
}

// Update methods for metrics

func (pe *PrometheusExporter) RecordRequest(duration time.Duration, success bool) {
	pe.requestsTotal.Add(1)
	if success {
		pe.requestsSuccess.Add(1)
	} else {
		pe.requestsFailed.Add(1)
	}
	pe.requestsDuration.Observe(float64(duration.Milliseconds()))
}

func (pe *PrometheusExporter) RecordEvents(count int64, bytes int64) {
	pe.eventsIngested.Add(count)
	pe.bytesIngested.Add(bytes)
}

func (pe *PrometheusExporter) RecordKafkaMetrics(sent, errors, lag int64) {
	pe.kafkaProducerSent.Add(sent)
	pe.kafkaProducerErrors.Add(errors)
	pe.kafkaLag.Store(lag)
}

func (pe *PrometheusExporter) RecordWatermark(min, max, lag int64, freshness float64) {
	pe.watermarkMin.Store(min)
	pe.watermarkMax.Store(max)
	pe.watermarkLag.Store(lag)
	pe.dataFreshness.Observe(freshness)
}

func (pe *PrometheusExporter) RecordSystemMetrics(cpu, memory float64, goroutines int32, connections int64) {
	pe.cpuUsage.Store(uint64(cpu * 100))
	pe.memoryUsage.Store(uint64(memory * 100))
	pe.goroutines.Store(goroutines)
	pe.openConnections.Store(connections)
}

func (pe *PrometheusExporter) RecordBackpressure(queueDepth, queueCap, shed int64, circuitOpen bool, rateHits int64) {
	pe.queueDepth.Store(queueDepth)
	pe.queueCapacity.Store(queueCap)
	pe.shedRequests.Add(shed)
	if circuitOpen {
		pe.circuitBreakerOpen.Store(1)
	} else {
		pe.circuitBreakerOpen.Store(0)
	}
	pe.rateLimitHits.Add(rateHits)
}

func (pe *PrometheusExporter) RecordCache(hits, misses, evictions, size int64) {
	pe.cacheHits.Add(hits)
	pe.cacheMisses.Add(misses)
	pe.cacheEvictions.Add(evictions)
	pe.cacheSize.Store(size)
}

func (pe *PrometheusExporter) RecordError(errorType string) {
	counter, _ := pe.errorsByType.LoadOrStore(errorType, &atomic.Int64{})
	counter.(*atomic.Int64).Add(1)
}