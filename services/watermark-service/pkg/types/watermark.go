package types

import "time"

// GlobalStats represents global watermark statistics
type GlobalStats struct {
	TotalServices   int       `json:"total_services"`
	TotalTables     int       `json:"total_tables"`
	TotalEvents     int64     `json:"total_events"`
	TotalPending    int64     `json:"total_pending"`
	TotalViolations int       `json:"total_violations"`
	LastUpdate      time.Time `json:"last_update"`
	UpdateCount     int64     `json:"update_count"`
}

// ServiceWatermark represents watermark data from a single service/shard
type ServiceWatermark struct {
	ServiceID       string    `json:"service_id"`       // e.g., "hot-tier-shard-0"
	ServiceType     string    `json:"service_type"`     // e.g., "hot-tier", "stream-ingester", "gateway"
	Namespace       string    `json:"namespace"`
	Table           string    `json:"table"`
	ShardID         int32     `json:"shard_id"`
	LowWatermark    int64     `json:"low_watermark_ms"`  // Oldest unprocessed event
	HighWatermark   int64     `json:"high_watermark_ms"` // Newest event seen
	EventCount      int64     `json:"event_count"`
	PendingCount    int64     `json:"pending_count"`
	LastUpdate      time.Time `json:"last_update"`
	ProcessingTime  int64     `json:"processing_time_ms"`
	IsHealthy       bool      `json:"is_healthy"`
}

// TableWatermark represents aggregated watermark for a table
type TableWatermark struct {
	Namespace          string             `json:"namespace"`
	Table              string             `json:"table"`
	MinWatermark       int64              `json:"min_watermark_ms"`       // Min across all shards
	MaxWatermark       int64              `json:"max_watermark_ms"`       // Max across all shards
	MedianWatermark    int64              `json:"median_watermark_ms"`
	ProcessingTime     int64              `json:"processing_time_ms"`
	FreshnessP50       int64              `json:"freshness_p50_ms"`
	FreshnessP95       int64              `json:"freshness_p95_ms"`
	FreshnessP99       int64              `json:"freshness_p99_ms"`
	EventsPerSecond    float64            `json:"events_per_second"`
	TotalEventCount    int64              `json:"total_event_count"`
	TotalPendingCount  int64              `json:"total_pending_count"`
	ActiveShards       int                `json:"active_shards"`
	HealthyShards      int                `json:"healthy_shards"`
	LastUpdate         time.Time          `json:"last_update"`
	ShardWatermarks    []ServiceWatermark `json:"shard_watermarks,omitempty"`
}

// SystemWatermark represents system-wide watermark state
type SystemWatermark struct {
	MinWatermark       int64                      `json:"min_watermark_ms"`
	MaxWatermark       int64                      `json:"max_watermark_ms"`
	ProcessingTime     int64                      `json:"processing_time_ms"`
	GlobalFreshnessP50 int64                      `json:"global_freshness_p50_ms"`
	GlobalFreshnessP95 int64                      `json:"global_freshness_p95_ms"`
	GlobalFreshnessP99 int64                      `json:"global_freshness_p99_ms"`
	TotalEventsPerSec  float64                    `json:"total_events_per_second"`
	TotalTables        int                        `json:"total_tables"`
	HealthyTables      int                        `json:"healthy_tables"`
	ViolatedSLOs       []SLOViolation             `json:"violated_slos,omitempty"`
	TableWatermarks    map[string]*TableWatermark `json:"table_watermarks,omitempty"`
	LastUpdate         time.Time                  `json:"last_update"`
}

// SLOViolation represents a freshness SLO violation
type SLOViolation struct {
	Namespace      string    `json:"namespace"`
	Table          string    `json:"table"`
	SLOTarget      int64     `json:"slo_target_ms"`
	ActualFreshness int64    `json:"actual_freshness_ms"`
	ViolationStart time.Time `json:"violation_start"`
	Duration       int64     `json:"duration_ms"`
	Severity       string    `json:"severity"` // "warning", "critical"
}

// WatermarkUpdate represents an update from a service
type WatermarkUpdate struct {
	ServiceID      string    `json:"service_id"`
	ServiceType    string    `json:"service_type"`
	Namespace      string    `json:"namespace"`
	Table          string    `json:"table"`
	ShardID        int32     `json:"shard_id"`
	EventTime      int64     `json:"event_time_ms"`
	ProcessingTime int64     `json:"processing_time_ms"`
	EventCount     int64     `json:"event_count"`
	PendingCount   int64     `json:"pending_count"`
	Timestamp      time.Time `json:"timestamp"`
}

// FreshnessConfig defines freshness SLO configuration
type FreshnessConfig struct {
	Namespace           string `json:"namespace"`
	Table               string `json:"table"`
	TargetFreshnessP50  int64  `json:"target_freshness_p50_ms"`
	TargetFreshnessP95  int64  `json:"target_freshness_p95_ms"`
	TargetFreshnessP99  int64  `json:"target_freshness_p99_ms"`
	AlertThreshold      int64  `json:"alert_threshold_ms"`
	CriticalThreshold   int64  `json:"critical_threshold_ms"`
}

// HealthStatus represents the health of watermark tracking
type HealthStatus struct {
	IsHealthy          bool      `json:"is_healthy"`
	LastUpdateTime     time.Time `json:"last_update_time"`
	StaleServices      []string  `json:"stale_services,omitempty"`
	UnhealthyTables    []string  `json:"unhealthy_tables,omitempty"`
	SystemFreshnessP99 int64     `json:"system_freshness_p99_ms"`
	Message            string    `json:"message"`
}