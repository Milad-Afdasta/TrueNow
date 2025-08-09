package metrics

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// MockCollector implements a mock metrics collector for testing
type MockCollector struct {
	config     CollectorConfig
	healthy    bool
	metrics    map[string]map[string]float64 // service -> metric -> value
	aggregator Aggregator
}

// NewMockCollector creates a new mock metrics collector
func NewMockCollector(config CollectorConfig) *MockCollector {
	return &MockCollector{
		config:     config,
		healthy:    true,
		metrics:    make(map[string]map[string]float64),
		aggregator: &SimpleAggregator{},
	}
}

// CollectServiceMetrics implements the Collector interface
func (m *MockCollector) CollectServiceMetrics(ctx context.Context, serviceName string, instances []*types.Instance) (ServiceMetrics, error) {
	if !m.healthy {
		return ServiceMetrics{}, fmt.Errorf("collector is unhealthy")
	}
	
	// Check for load file to make metrics responsive to external load
	var loadMultiplier float64 = 0.1  // Default to low load
	if loadData, err := os.ReadFile("/tmp/current_load.txt"); err == nil {
		if loadVal, err := strconv.ParseFloat(strings.TrimSpace(string(loadData)), 64); err == nil {
			// Convert load percentage directly (0-100% -> 0.0-1.0)
			loadMultiplier = loadVal / 100.0
			fmt.Printf("DEBUG: Load from file: %.2f, multiplier: %.2f\n", loadVal, loadMultiplier)
		}
	}
	
	// Generate mock metrics
	metrics := make(map[string]MetricValue)
	
	// Check if we have preset values for this service
	if serviceMetrics, ok := m.metrics[serviceName]; ok && len(serviceMetrics) > 0 {
		// Use preset values
		if cpuVal, ok := serviceMetrics["cpu"]; ok {
			metrics["cpu"] = MetricValue{
				Value:       cpuVal,
				Unit:        "percent",
				Aggregation: types.AggregationAvg,
				Window:      time.Minute,
			}
		}
	
		// Memory metric
		if memVal, ok := serviceMetrics["memory"]; ok {
			metrics["memory"] = MetricValue{
				Value:       memVal,
				Unit:        "percent",
				Aggregation: types.AggregationMax,
				Window:      time.Minute,
			}
		}
		
		// Request rate metric
		if serviceName == "gateway" {
			if reqVal, ok := serviceMetrics["request_rate"]; ok {
				metrics["request_rate"] = MetricValue{
					Value:       reqVal,
					Unit:        "req/s",
					Aggregation: types.AggregationSum,
					Window:      30 * time.Second,
				}
			}
			
			// Response time metric
			if respVal, ok := serviceMetrics["response_time"]; ok {
				metrics["response_time"] = MetricValue{
					Value:       respVal,
					Unit:        "ms",
					Aggregation: types.AggregationP95,
					Window:      time.Minute,
				}
			}
		}
		
		// Kafka lag metric for processor/aggregator
		if serviceName == "processor" || serviceName == "aggregator" {
			if lagVal, ok := serviceMetrics["kafka_lag"]; ok {
				metrics["kafka_lag"] = MetricValue{
					Value:       lagVal,
					Unit:        "messages",
					Aggregation: types.AggregationMax,
					Window:      2 * time.Minute,
				}
			}
		}
		
		// Queue depth for aggregator
		if serviceName == "aggregator" {
			if queueVal, ok := serviceMetrics["queue_depth"]; ok {
				metrics["queue_depth"] = MetricValue{
					Value:       queueVal,
					Unit:        "messages",
					Aggregation: types.AggregationMax,
					Window:      30 * time.Second,
				}
			}
		}
	}
	
	// Generate load-responsive defaults if metrics weren't preset
	if _, hasMetric := metrics["cpu"]; !hasMetric {
		cpuValues := make([]float64, len(instances))
		for i := range instances {
			// CPU should be low at low load, high at high load
			// At 10% load -> ~17% CPU, at 100% load -> ~87% CPU
			baseCPU := 10 + rand.Float64()*5 // 10-15% base
			cpuValues[i] = baseCPU + (loadMultiplier * 70) // Scale based on load
		}
		metrics["cpu"] = MetricValue{
			Value:       m.aggregator.Aggregate(cpuValues, types.AggregationAvg),
			Unit:        "percent",
			Aggregation: types.AggregationAvg,
			Window:      time.Minute,
		}
	}
	
	if _, hasMetric := metrics["memory"]; !hasMetric {
		memValues := make([]float64, len(instances))
		for i := range instances {
			// Memory should be low at low load, high at high load
			// At 10% load -> ~21% memory, at 100% load -> ~86% memory
			baseMemory := 15 + rand.Float64()*5 // 15-20% base
			memValues[i] = baseMemory + (loadMultiplier * 65) // Scale based on load
		}
		metrics["memory"] = MetricValue{
			Value:       m.aggregator.Aggregate(memValues, types.AggregationMax),
			Unit:        "percent",
			Aggregation: types.AggregationMax,
			Window:      time.Minute,
		}
	}
	
	// Service-specific defaults if not already set
	if serviceName == "gateway" {
		if _, hasMetric := metrics["request_rate"]; !hasMetric {
			// Request rate scales with load
			baseRate := 100 + rand.Float64()*100 // 100-200 base
			metrics["request_rate"] = MetricValue{
				Value:       baseRate + (loadMultiplier * 9800), // Up to 10000 at full load
				Unit:        "req/s",
				Aggregation: types.AggregationSum,
				Window:      30 * time.Second,
			}
		}
		
		if _, hasMetric := metrics["response_time"]; !hasMetric {
			// Response time increases with load
			baseTime := 10 + rand.Float64()*10 // 10-20ms base
			metrics["response_time"] = MetricValue{
				Value:       baseTime + (loadMultiplier * 180), // Up to 200ms at full load
				Unit:        "ms",
				Aggregation: types.AggregationP95,
				Window:      time.Minute,
			}
		}
	}
	
	if serviceName == "processor" || serviceName == "aggregator" {
		if _, hasMetric := metrics["kafka_lag"]; !hasMetric {
			// Kafka lag increases with load
			metrics["kafka_lag"] = MetricValue{
				Value:       loadMultiplier * 100000, // 0-100k messages based on load
				Unit:        "messages",
				Aggregation: types.AggregationMax,
				Window:      2 * time.Minute,
			}
		}
	}
	
	if serviceName == "aggregator" {
		if _, hasMetric := metrics["queue_depth"]; !hasMetric {
			// Queue depth increases with load
			metrics["queue_depth"] = MetricValue{
				Value:       loadMultiplier * 20000, // 0-20k messages based on load
				Unit:        "messages",
				Aggregation: types.AggregationMax,
				Window:      30 * time.Second,
			}
		}
	}

	return ServiceMetrics{
		ServiceName: serviceName,
		Timestamp:   time.Now(),
		Metrics:     metrics,
		Instances:   len(instances),
	}, nil
}

// CollectInstanceMetrics implements the Collector interface
func (m *MockCollector) CollectInstanceMetrics(ctx context.Context, instance *types.Instance) (InstanceMetrics, error) {
	if !m.healthy {
		return InstanceMetrics{}, fmt.Errorf("collector is unhealthy")
	}
	
	// Check for load file to make instance metrics responsive too
	var loadMultiplier float64 = 0.5  // Default to medium load for instances
	if loadData, err := os.ReadFile("/tmp/current_load.txt"); err == nil {
		if loadVal, err := strconv.ParseFloat(strings.TrimSpace(string(loadData)), 64); err == nil {
			loadMultiplier = loadVal / 100.0
		}
	}
	
	// Generate mock metrics for instance
	metrics := make(map[string]MetricValue)
	
	// CPU varies with load
	baseCPU := 20 + rand.Float64()*10 // 20-30% base
	metrics["cpu"] = MetricValue{
		Value: baseCPU + (loadMultiplier * 60), // Up to 90% at full load
		Unit:  "percent",
	}
	
	// Memory varies with load
	baseMemory := 20 + rand.Float64()*10 // 20-30% base
	metrics["memory"] = MetricValue{
		Value: baseMemory + (loadMultiplier * 60), // Up to 90% at full load
		Unit:  "percent",
	}
	
	// Connections vary with load
	metrics["connections"] = MetricValue{
		Value: loadMultiplier * 1000, // 0-1000 connections based on load
		Unit:  "count",
	}
	
	return InstanceMetrics{
		InstanceID: instance.ID,
		Timestamp:  time.Now(),
		Metrics:    metrics,
		Healthy:    instance.IsHealthy(),
	}, nil
}

// QueryCustomMetric implements the Collector interface
func (m *MockCollector) QueryCustomMetric(ctx context.Context, query string, window time.Duration) (float64, error) {
	if !m.healthy {
		return 0, fmt.Errorf("collector is unhealthy")
	}
	
	// Return a random value for custom queries
	return rand.Float64() * 1000, nil
}

// IsHealthy implements the Collector interface
func (m *MockCollector) IsHealthy(ctx context.Context) bool {
	return m.healthy
}

// Close implements the Collector interface
func (m *MockCollector) Close() error {
	m.healthy = false
	return nil
}

// SetHealthy sets the health status of the mock collector
func (m *MockCollector) SetHealthy(healthy bool) {
	m.healthy = healthy
}

// SetMetricValue sets a specific metric value for testing
func (m *MockCollector) SetMetricValue(serviceName, metricName string, value float64) {
	if m.metrics[serviceName] == nil {
		m.metrics[serviceName] = make(map[string]float64)
	}
	m.metrics[serviceName][metricName] = value
}

// GetMetricValue gets a specific metric value
func (m *MockCollector) GetMetricValue(serviceName, metricName string) (float64, bool) {
	if serviceMetrics, ok := m.metrics[serviceName]; ok {
		if value, ok := serviceMetrics[metricName]; ok {
			return value, true
		}
	}
	return 0, false
}