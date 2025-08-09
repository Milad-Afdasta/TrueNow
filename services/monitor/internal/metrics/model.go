// Package metrics defines the comprehensive metrics model for TrueNow Analytics Platform
package metrics

import (
	"time"
)

// ServiceMetrics represents complete metrics for a service
type ServiceMetrics struct {
	ServiceName string
	Timestamp   time.Time
	
	// Instance Management
	Instances       int
	HealthyInstances int
	UnhealthyInstances int
	
	// Throughput Metrics
	RequestsPerSecond    float64
	EventsPerSecond      float64
	BytesInPerSecond     float64
	BytesOutPerSecond    float64
	
	// Latency Percentiles (in milliseconds)
	LatencyP50  float64
	LatencyP90  float64
	LatencyP95  float64
	LatencyP99  float64
	LatencyP999 float64
	LatencyMax  float64
	LatencyMean float64
	
	// Error Metrics
	ErrorRate        float64  // Errors per second
	ErrorPercentage  float64  // Percentage of requests that are errors
	TimeoutRate      float64
	RejectionRate    float64
	
	// Resource Utilization
	CPUUsagePercent     float64
	MemoryUsagePercent  float64
	MemoryUsageBytes    int64
	HeapUsageBytes      int64
	GoroutineCount      int
	GCPauseP99          float64  // GC pause time p99 in ms
	
	// Connection Metrics
	ActiveConnections   int
	ConnectionPoolSize  int
	ConnectionErrors    int
	
	// Service-Specific Metrics
	CustomMetrics map[string]interface{}
}

// HotTierMetrics represents metrics specific to hot-tier service
type HotTierMetrics struct {
	ServiceMetrics
	
	// Ring Buffer Metrics
	Buffer1s  BufferMetrics
	Buffer10s BufferMetrics
	Buffer1m  BufferMetrics
	
	// Deduplication
	EventsProcessed int64
	EventsDeduped   int64
	DedupeRatio     float64
	
	// Sketch Accuracy
	HLLAccuracy      float64  // HyperLogLog accuracy
	TDigestAccuracy  float64  // T-Digest accuracy
	
	// NUMA Optimization
	NumaNode        int
	LocalMemAccess  float64  // Percentage of local NUMA access
	RemoteMemAccess float64  // Percentage of remote NUMA access
}

// BufferMetrics represents ring buffer statistics
type BufferMetrics struct {
	Resolution   string  // "1s", "10s", "1m"
	SlotsUsed    int64
	TotalSlots   int64
	Utilization  float64  // Percentage
	WritesTotal  int64
	ReadsTotal   int64
	WriteThroughput float64  // Writes per second
	ReadThroughput  float64  // Reads per second
}

// GatewayMetrics represents gateway-specific metrics
type GatewayMetrics struct {
	ServiceMetrics
	
	// Ingestion
	BatchSize           int
	CompressionRatio    float64
	ValidationFailures  int64
	RateLimitHits       int64
	
	// Kafka Production
	KafkaLag           int64
	KafkaOffset        int64
	KafkaProduceErrors int64
	KafkaBackpressure  bool
}

// QueryAPIMetrics represents query API metrics
type QueryAPIMetrics struct {
	ServiceMetrics
	
	// Query Performance
	QueriesPerSecond   float64
	CacheHitRate       float64
	QueryComplexity    float64  // Average complexity score
	FanoutFactor       float64  // Average number of shards queried
	
	// Redis Cache
	RedisConnections   int
	RedisMemoryUsage   int64
	RedisEvictions     int64
	RedisHitRate       float64
}

// StreamIngesterMetrics represents stream ingester metrics
type StreamIngesterMetrics struct {
	ServiceMetrics
	
	// Kafka Consumption
	ConsumerLag        int64
	ConsumerOffset     int64
	ConsumerErrors     int64
	RebalanceCount     int
	
	// Transformation
	TransformErrors    int64
	TransformLatency   float64
	BatchingEfficiency float64
}

// WatermarkMetrics represents watermark service metrics
type WatermarkMetrics struct {
	ServiceMetrics
	
	// Freshness Tracking
	GlobalFreshnessP50 int64  // milliseconds
	GlobalFreshnessP95 int64
	GlobalFreshnessP99 int64
	
	// Table Tracking
	TablesTracked      int
	StalePartitions    int
	FreshnessViolations int
	
	// SLO Compliance
	SLOCompliance      float64  // Percentage meeting SLO
	SLOViolations      []SLOViolation
}

// SLOViolation represents an SLO violation event
type SLOViolation struct {
	Timestamp   time.Time
	MetricName  string
	Threshold   float64
	ActualValue float64
	Duration    time.Duration
	Severity    string  // "warning", "critical"
}

// RebalancerMetrics represents rebalancer service metrics
type RebalancerMetrics struct {
	ServiceMetrics
	
	// Shard Management
	TotalShards        int
	ActiveShards       int
	RebalancingShards  int
	
	// Load Distribution
	GiniCoefficient    float64  // Load imbalance measure
	MaxShardLoad       float64
	MinShardLoad       float64
	AvgShardLoad       float64
	
	// Rebalance Operations
	RebalancesPending  int
	RebalancesActive   int
	RebalancesFailed   int
	LastRebalanceTime  time.Time
}

// AutoscalerMetrics represents autoscaler metrics
type AutoscalerMetrics struct {
	ServiceMetrics
	
	// Scaling Decisions
	ScaleUpEvents      int
	ScaleDownEvents    int
	ScaleBlockedEvents int
	
	// Resource Tracking
	TotalCapacity      float64
	UsedCapacity       float64
	ReservedCapacity   float64
	
	// Cost Metrics
	EstimatedCostHour  float64
	ProjectedCostDay   float64
	CostEfficiency     float64  // Events per dollar
}

// SystemMetrics represents overall system metrics
type SystemMetrics struct {
	Timestamp time.Time
	
	// Aggregate Performance
	TotalEventsPerSecond  float64
	TotalQueriesPerSecond float64
	TotalBytesPerSecond   float64
	
	// System Health
	HealthScore          float64  // 0-100
	ServiceAvailability  map[string]bool
	CriticalAlerts       int
	WarningAlerts        int
	
	// Resource Totals
	TotalCPUCores        int
	TotalMemoryGB        float64
	TotalDiskGB          float64
	
	// Cost & Efficiency
	CostPerMillion       float64  // Cost per million events
	EfficiencyScore      float64  // 0-100
}

// Alert represents a system alert
type Alert struct {
	ID          string
	Timestamp   time.Time
	Service     string
	Severity    string  // "info", "warning", "critical"
	Message     string
	Value       float64
	Threshold   float64
	Duration    time.Duration
	Resolved    bool
	ResolvedAt  time.Time
}