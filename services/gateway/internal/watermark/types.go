package watermark

// WatermarkState represents the current state of watermarks
type WatermarkState struct {
	MinWatermark    int64                    `json:"min_watermark"`
	MaxWatermark    int64                    `json:"max_watermark"`
	ProcessingTime  int64                    `json:"processing_time"`
	EventTime       int64                    `json:"event_time"`
	WatermarkLag    int64                    `json:"watermark_lag_ms"`
	PartitionCount  int32                    `json:"partition_count"`
	LastUpdate      int64                    `json:"last_update"`
	UpdateCount     int64                    `json:"update_count"`
	PartitionStates map[int32]PartitionState `json:"partitions"`
}

// PartitionState represents watermark state for a partition
type PartitionState struct {
	LowWatermark    int64 `json:"low_watermark"`
	HighWatermark   int64 `json:"high_watermark"`
	CommittedOffset int64 `json:"committed_offset"`
	PendingCount    int64 `json:"pending_count"`
	LastUpdate      int64 `json:"last_update"`
}

// WatermarkMetrics contains watermark metrics for monitoring
type WatermarkMetrics struct {
	// Freshness metrics
	P50Freshness int64 `json:"p50_freshness_ms"`
	P95Freshness int64 `json:"p95_freshness_ms"`
	P99Freshness int64 `json:"p99_freshness_ms"`
	MaxFreshness int64 `json:"max_freshness_ms"`
	
	// Throughput metrics
	EventsPerSecond     float64 `json:"events_per_second"`
	BytesPerSecond      float64 `json:"bytes_per_second"`
	PartitionsPerSecond float64 `json:"partitions_per_second"`
	
	// Lag metrics
	TotalLag        int64 `json:"total_lag"`
	AverageLag      int64 `json:"average_lag_ms"`
	MaxPartitionLag int64 `json:"max_partition_lag_ms"`
	
	// Health indicators
	IsHealthy           bool    `json:"is_healthy"`
	HealthScore         float64 `json:"health_score"` // 0-100
	StalledPartitions   int32   `json:"stalled_partitions"`
	BackpressureActive  bool    `json:"backpressure_active"`
}

// WindowType defines the type of windowing for watermarks
type WindowType int

const (
	// TumblingWindow fixed-size, non-overlapping windows
	TumblingWindow WindowType = iota
	
	// SlidingWindow fixed-size, overlapping windows
	SlidingWindow
	
	// SessionWindow dynamic-size windows based on activity gaps
	SessionWindow
)

// Window represents a time window for watermark tracking
type Window struct {
	Type      WindowType `json:"type"`
	StartTime int64      `json:"start_time"`
	EndTime   int64      `json:"end_time"`
	Size      int64      `json:"size_ms"`
	Slide     int64      `json:"slide_ms"` // For sliding windows
	Gap       int64      `json:"gap_ms"`   // For session windows
	
	// Window state
	EventCount   int64   `json:"event_count"`
	BytesCount   int64   `json:"bytes_count"`
	MinEventTime int64   `json:"min_event_time"`
	MaxEventTime int64   `json:"max_event_time"`
	Watermark    int64   `json:"watermark"`
	IsComplete   bool    `json:"is_complete"`
	IsFired      bool    `json:"is_fired"`
	
	// Late event handling
	LateEventCount     int64 `json:"late_event_count"`
	AllowedLateness    int64 `json:"allowed_lateness_ms"`
	DroppedEventCount  int64 `json:"dropped_event_count"`
}