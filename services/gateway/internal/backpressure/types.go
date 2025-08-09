package backpressure

import "errors"

// Priority levels for request queuing
type Priority int

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical // For health checks and admin operations
)

// Errors
var (
	ErrQueueFull        = errors.New("request queue is full")
	ErrTimeout          = errors.New("request timeout")
	ErrInvalidPriority  = errors.New("invalid priority level")
	ErrShuttingDown     = errors.New("service is shutting down")
	ErrRateLimited      = errors.New("rate limit exceeded")
	ErrCircuitOpen      = errors.New("circuit breaker is open")
)

// QueueMetrics contains queue statistics
type QueueMetrics struct {
	Queued     int64
	Processed  int64
	Dropped    int64
	TimedOut   int64
	QueueSizes map[string]int
}

// AdaptiveConfig contains adaptive backpressure configuration
type AdaptiveConfig struct {
	// Target latency in milliseconds
	TargetLatencyMs int64
	
	// Target success rate (0-100)
	TargetSuccessRate float64
	
	// Queue size limits
	MinQueueSize int64
	MaxQueueSize int64
	
	// Rate limit bounds
	MinRateLimit int64
	MaxRateLimit int64
	
	// Adjustment factors
	IncreaseRate float64 // How fast to increase limits (e.g., 1.1 = 10% increase)
	DecreaseRate float64 // How fast to decrease limits (e.g., 0.9 = 10% decrease)
	
	// Evaluation window
	WindowSize int // Number of samples to consider
}