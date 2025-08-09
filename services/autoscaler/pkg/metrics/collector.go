package metrics

import (
	"context"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/autoscaler/pkg/types"
)

// Collector defines the interface for collecting metrics
type Collector interface {
	// CollectServiceMetrics collects metrics for a specific service
	CollectServiceMetrics(ctx context.Context, serviceName string, instances []*types.Instance) (ServiceMetrics, error)
	
	// CollectInstanceMetrics collects metrics for a specific instance
	CollectInstanceMetrics(ctx context.Context, instance *types.Instance) (InstanceMetrics, error)
	
	// QueryCustomMetric executes a custom metric query
	QueryCustomMetric(ctx context.Context, query string, window time.Duration) (float64, error)
	
	// IsHealthy checks if the metrics collector is healthy
	IsHealthy(ctx context.Context) bool
	
	// Close closes the metrics collector
	Close() error
}

// ServiceMetrics represents aggregated metrics for a service
type ServiceMetrics struct {
	ServiceName string                 `json:"service_name"`
	Timestamp   time.Time              `json:"timestamp"`
	Metrics     map[string]MetricValue `json:"metrics"`
	Instances   int                    `json:"instance_count"`
}

// InstanceMetrics represents metrics for a single instance
type InstanceMetrics struct {
	InstanceID string                 `json:"instance_id"`
	Timestamp  time.Time              `json:"timestamp"`
	Metrics    map[string]MetricValue `json:"metrics"`
	Healthy    bool                   `json:"healthy"`
}

// MetricValue represents a metric value with metadata
type MetricValue struct {
	Value       float64              `json:"value"`
	Unit        string               `json:"unit,omitempty"`
	Aggregation types.Aggregation    `json:"aggregation,omitempty"`
	Window      time.Duration        `json:"window,omitempty"`
	Labels      map[string]string    `json:"labels,omitempty"`
}

// MetricQuery represents a metric query
type MetricQuery struct {
	Type        types.MetricType  `json:"type"`
	ServiceName string            `json:"service_name,omitempty"`
	InstanceID  string            `json:"instance_id,omitempty"`
	Aggregation types.Aggregation `json:"aggregation"`
	Window      time.Duration     `json:"window"`
	Query       string            `json:"query,omitempty"` // For custom queries
}

// MetricResult represents the result of a metric query
type MetricResult struct {
	Query     MetricQuery `json:"query"`
	Value     float64     `json:"value"`
	Timestamp time.Time   `json:"timestamp"`
	Error     error       `json:"error,omitempty"`
}

// CollectorConfig represents collector configuration
type CollectorConfig struct {
	Endpoint        string        `json:"endpoint" yaml:"endpoint"`
	Timeout         time.Duration `json:"timeout" yaml:"timeout"`
	RetryAttempts   int           `json:"retry_attempts" yaml:"retry_attempts"`
	RetryDelay      time.Duration `json:"retry_delay" yaml:"retry_delay"`
	CacheEnabled    bool          `json:"cache_enabled" yaml:"cache_enabled"`
	CacheTTL        time.Duration `json:"cache_ttl" yaml:"cache_ttl"`
	MaxConcurrency  int           `json:"max_concurrency" yaml:"max_concurrency"`
}

// DefaultCollectorConfig returns default collector configuration
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{
		Timeout:        10 * time.Second,
		RetryAttempts:  3,
		RetryDelay:     1 * time.Second,
		CacheEnabled:   true,
		CacheTTL:       15 * time.Second,
		MaxConcurrency: 10,
	}
}

// Aggregator defines methods for aggregating metrics
type Aggregator interface {
	// Aggregate aggregates metric values based on the aggregation type
	Aggregate(values []float64, aggregation types.Aggregation) float64
}

// SimpleAggregator implements basic aggregation functions
type SimpleAggregator struct{}

// Aggregate implements the Aggregator interface
func (a *SimpleAggregator) Aggregate(values []float64, aggregation types.Aggregation) float64 {
	if len(values) == 0 {
		return 0
	}
	
	switch aggregation {
	case types.AggregationAvg:
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return sum / float64(len(values))
		
	case types.AggregationMax:
		max := values[0]
		for _, v := range values {
			if v > max {
				max = v
			}
		}
		return max
		
	case types.AggregationMin:
		min := values[0]
		for _, v := range values {
			if v < min {
				min = v
			}
		}
		return min
		
	case types.AggregationSum:
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return sum
		
	case types.AggregationP95:
		return percentile(values, 0.95)
		
	case types.AggregationP99:
		return percentile(values, 0.99)
		
	default:
		// Default to average
		return a.Aggregate(values, types.AggregationAvg)
	}
}

// percentile calculates the percentile value
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	
	// Simple implementation - should be improved with proper sorting
	// For now, just return the max value as an approximation
	max := values[0]
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	return max * p
}

// MetricCache defines a cache for metrics
type MetricCache interface {
	// Get retrieves a cached metric value
	Get(key string) (MetricValue, bool)
	
	// Set stores a metric value in the cache
	Set(key string, value MetricValue, ttl time.Duration)
	
	// Delete removes a metric from the cache
	Delete(key string)
	
	// Clear clears all cached metrics
	Clear()
}

// InMemoryCache implements a simple in-memory cache
type InMemoryCache struct {
	data map[string]cacheEntry
}

type cacheEntry struct {
	value      MetricValue
	expiration time.Time
}

// NewInMemoryCache creates a new in-memory cache
func NewInMemoryCache() *InMemoryCache {
	return &InMemoryCache{
		data: make(map[string]cacheEntry),
	}
}

// Get retrieves a cached metric value
func (c *InMemoryCache) Get(key string) (MetricValue, bool) {
	entry, exists := c.data[key]
	if !exists {
		return MetricValue{}, false
	}
	
	if time.Now().After(entry.expiration) {
		delete(c.data, key)
		return MetricValue{}, false
	}
	
	return entry.value, true
}

// Set stores a metric value in the cache
func (c *InMemoryCache) Set(key string, value MetricValue, ttl time.Duration) {
	c.data[key] = cacheEntry{
		value:      value,
		expiration: time.Now().Add(ttl),
	}
}

// Delete removes a metric from the cache
func (c *InMemoryCache) Delete(key string) {
	delete(c.data, key)
}

// Clear clears all cached metrics
func (c *InMemoryCache) Clear() {
	c.data = make(map[string]cacheEntry)
}