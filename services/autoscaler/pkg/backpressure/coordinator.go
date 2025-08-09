package backpressure

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// BackpressureLevel represents the current backpressure level
type BackpressureLevel int

const (
	LevelNone BackpressureLevel = iota
	LevelLow
	LevelMedium
	LevelHigh
	LevelCritical
)

func (l BackpressureLevel) String() string {
	switch l {
	case LevelNone:
		return "none"
	case LevelLow:
		return "low"
	case LevelMedium:
		return "medium"
	case LevelHigh:
		return "high"
	case LevelCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// BackpressureConfig defines backpressure configuration
type BackpressureConfig struct {
	// Thresholds for different levels (percentage of max scale)
	LowThreshold      float64 `json:"low_threshold"`      // e.g., 60%
	MediumThreshold   float64 `json:"medium_threshold"`   // e.g., 75%
	HighThreshold     float64 `json:"high_threshold"`     // e.g., 85%
	CriticalThreshold float64 `json:"critical_threshold"` // e.g., 95%
	
	// Response strategies per level
	LowResponse      ResponseStrategy `json:"low_response"`
	MediumResponse   ResponseStrategy `json:"medium_response"`
	HighResponse     ResponseStrategy `json:"high_response"`
	CriticalResponse ResponseStrategy `json:"critical_response"`
	
	// Evaluation interval
	EvaluationInterval time.Duration `json:"evaluation_interval"`
	
	// Smoothing window for level changes
	SmoothingWindow time.Duration `json:"smoothing_window"`
}

// ResponseStrategy defines how to respond to backpressure
type ResponseStrategy struct {
	RateLimitReduction float64       `json:"rate_limit_reduction"` // Percentage to reduce rate limit
	RejectPercentage   float64       `json:"reject_percentage"`     // Percentage of requests to reject
	QueueSizeLimit     int           `json:"queue_size_limit"`      // Max queue size
	Timeout            time.Duration `json:"timeout"`               // Request timeout
	CircuitBreaker     bool          `json:"circuit_breaker"`       // Enable circuit breaker
}

// BackpressureCoordinator coordinates backpressure across services
type BackpressureCoordinator struct {
	config    BackpressureConfig
	mu        sync.RWMutex
	
	// Current state
	currentLevel    BackpressureLevel
	serviceStates   map[string]*ServiceBackpressureState
	
	// Metrics
	levelChanges    uint64
	requestsDropped uint64
	
	// Callbacks
	onLevelChange func(old, new BackpressureLevel)
	
	// Control
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// ServiceBackpressureState tracks backpressure state for a service
type ServiceBackpressureState struct {
	ServiceName      string
	CurrentInstances int
	MaxInstances     int
	ScalePercentage  float64
	IsAtMaxScale     bool
	LastUpdated      time.Time
	
	// Metrics
	QueueDepth       int
	RequestRate      float64
	ErrorRate        float64
	ResponseTime     time.Duration
}

// NewBackpressureCoordinator creates a new backpressure coordinator
func NewBackpressureCoordinator(config BackpressureConfig) *BackpressureCoordinator {
	if config.EvaluationInterval == 0 {
		config.EvaluationInterval = 5 * time.Second
	}
	if config.SmoothingWindow == 0 {
		config.SmoothingWindow = 30 * time.Second
	}
	
	// Set default thresholds if not provided
	if config.LowThreshold == 0 {
		config.LowThreshold = 0.6
	}
	if config.MediumThreshold == 0 {
		config.MediumThreshold = 0.75
	}
	if config.HighThreshold == 0 {
		config.HighThreshold = 0.85
	}
	if config.CriticalThreshold == 0 {
		config.CriticalThreshold = 0.95
	}
	
	return &BackpressureCoordinator{
		config:        config,
		currentLevel:  LevelNone,
		serviceStates: make(map[string]*ServiceBackpressureState),
		stopChan:      make(chan struct{}),
	}
}

// Start starts the backpressure coordinator
func (bc *BackpressureCoordinator) Start(ctx context.Context) error {
	bc.wg.Add(1)
	go bc.evaluationLoop(ctx)
	return nil
}

// Stop stops the backpressure coordinator
func (bc *BackpressureCoordinator) Stop() error {
	close(bc.stopChan)
	bc.wg.Wait()
	return nil
}

// UpdateServiceState updates the state of a service
func (bc *BackpressureCoordinator) UpdateServiceState(state *ServiceBackpressureState) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	
	state.LastUpdated = time.Now()
	state.ScalePercentage = float64(state.CurrentInstances) / float64(state.MaxInstances)
	state.IsAtMaxScale = state.CurrentInstances >= state.MaxInstances
	
	bc.serviceStates[state.ServiceName] = state
}

// GetCurrentLevel returns the current backpressure level
func (bc *BackpressureCoordinator) GetCurrentLevel() BackpressureLevel {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.currentLevel
}

// GetResponseStrategy returns the response strategy for the current level
func (bc *BackpressureCoordinator) GetResponseStrategy() ResponseStrategy {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	
	switch bc.currentLevel {
	case LevelLow:
		return bc.config.LowResponse
	case LevelMedium:
		return bc.config.MediumResponse
	case LevelHigh:
		return bc.config.HighResponse
	case LevelCritical:
		return bc.config.CriticalResponse
	default:
		return ResponseStrategy{}
	}
}

// evaluationLoop continuously evaluates backpressure levels
func (bc *BackpressureCoordinator) evaluationLoop(ctx context.Context) {
	defer bc.wg.Done()
	
	ticker := time.NewTicker(bc.config.EvaluationInterval)
	defer ticker.Stop()
	
	levelHistory := make([]BackpressureLevel, 0)
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-bc.stopChan:
			return
		case <-ticker.C:
			newLevel := bc.calculateLevel()
			
			// Add to history for smoothing
			levelHistory = append(levelHistory, newLevel)
			
			// Keep only recent history
			windowSize := int(bc.config.SmoothingWindow / bc.config.EvaluationInterval)
			if len(levelHistory) > windowSize {
				levelHistory = levelHistory[len(levelHistory)-windowSize:]
			}
			
			// Calculate smoothed level (majority in window)
			smoothedLevel := bc.getSmoothedLevel(levelHistory)
			
			// Update if changed
			bc.mu.Lock()
			oldLevel := bc.currentLevel
			if smoothedLevel != oldLevel {
				bc.currentLevel = smoothedLevel
				atomic.AddUint64(&bc.levelChanges, 1)
				
				if bc.onLevelChange != nil {
					go bc.onLevelChange(oldLevel, smoothedLevel)
				}
			}
			bc.mu.Unlock()
		}
	}
}

// calculateLevel calculates the backpressure level based on service states
func (bc *BackpressureCoordinator) calculateLevel() BackpressureLevel {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	
	if len(bc.serviceStates) == 0 {
		return LevelNone
	}
	
	// Calculate weighted average of scale percentages
	var totalWeight float64
	var weightedSum float64
	
	for _, state := range bc.serviceStates {
		// Weight critical services higher
		weight := 1.0
		if state.ServiceName == "gateway" {
			weight = 2.0 // Gateway is more critical
		}
		
		totalWeight += weight
		weightedSum += state.ScalePercentage * weight
	}
	
	if totalWeight == 0 {
		return LevelNone
	}
	
	avgScalePercentage := weightedSum / totalWeight
	
	// Determine level based on thresholds
	switch {
	case avgScalePercentage >= bc.config.CriticalThreshold:
		return LevelCritical
	case avgScalePercentage >= bc.config.HighThreshold:
		return LevelHigh
	case avgScalePercentage >= bc.config.MediumThreshold:
		return LevelMedium
	case avgScalePercentage >= bc.config.LowThreshold:
		return LevelLow
	default:
		return LevelNone
	}
}

// getSmoothedLevel returns the most common level in the history
func (bc *BackpressureCoordinator) getSmoothedLevel(history []BackpressureLevel) BackpressureLevel {
	if len(history) == 0 {
		return LevelNone
	}
	
	// Count occurrences
	counts := make(map[BackpressureLevel]int)
	for _, level := range history {
		counts[level]++
	}
	
	// Find most common
	var maxCount int
	var mostCommon BackpressureLevel
	for level, count := range counts {
		if count > maxCount {
			maxCount = count
			mostCommon = level
		}
	}
	
	return mostCommon
}

// SetLevelChangeCallback sets the callback for level changes
func (bc *BackpressureCoordinator) SetLevelChangeCallback(callback func(old, new BackpressureLevel)) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.onLevelChange = callback
}

// GetServiceStates returns current service states
func (bc *BackpressureCoordinator) GetServiceStates() map[string]*ServiceBackpressureState {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	
	states := make(map[string]*ServiceBackpressureState)
	for k, v := range bc.serviceStates {
		states[k] = v
	}
	return states
}

// GetMetrics returns backpressure metrics
func (bc *BackpressureCoordinator) GetMetrics() BackpressureMetrics {
	return BackpressureMetrics{
		CurrentLevel:    bc.GetCurrentLevel(),
		LevelChanges:    atomic.LoadUint64(&bc.levelChanges),
		RequestsDropped: atomic.LoadUint64(&bc.requestsDropped),
		ServiceCount:    len(bc.serviceStates),
	}
}

// BackpressureMetrics contains backpressure metrics
type BackpressureMetrics struct {
	CurrentLevel    BackpressureLevel `json:"current_level"`
	LevelChanges    uint64            `json:"level_changes"`
	RequestsDropped uint64            `json:"requests_dropped"`
	ServiceCount    int               `json:"service_count"`
}

// ShouldDropRequest determines if a request should be dropped
func (bc *BackpressureCoordinator) ShouldDropRequest() bool {
	strategy := bc.GetResponseStrategy()
	if strategy.RejectPercentage == 0 {
		return false
	}
	
	// Simple probabilistic dropping using nanosecond variation
	// In production, use a more sophisticated algorithm
	randValue := (time.Now().UnixNano() / 1000) % 100
	shouldDrop := float64(randValue) < strategy.RejectPercentage
	
	if shouldDrop {
		atomic.AddUint64(&bc.requestsDropped, 1)
	}
	
	return shouldDrop
}

// GetRateLimit returns the adjusted rate limit based on backpressure
func (bc *BackpressureCoordinator) GetRateLimit(baseLimit int) int {
	strategy := bc.GetResponseStrategy()
	if strategy.RateLimitReduction == 0 {
		return baseLimit
	}
	
	reduction := 1.0 - (strategy.RateLimitReduction / 100.0)
	return int(float64(baseLimit) * reduction)
}

// DefaultBackpressureConfig returns a default configuration
func DefaultBackpressureConfig() BackpressureConfig {
	return BackpressureConfig{
		LowThreshold:      0.6,
		MediumThreshold:   0.75,
		HighThreshold:     0.85,
		CriticalThreshold: 0.95,
		
		LowResponse: ResponseStrategy{
			RateLimitReduction: 10, // Reduce rate limit by 10%
			RejectPercentage:   0,  // Don't reject requests yet
			QueueSizeLimit:     1000,
			Timeout:            10 * time.Second,
		},
		
		MediumResponse: ResponseStrategy{
			RateLimitReduction: 25, // Reduce rate limit by 25%
			RejectPercentage:   5,  // Reject 5% of requests
			QueueSizeLimit:     500,
			Timeout:            5 * time.Second,
		},
		
		HighResponse: ResponseStrategy{
			RateLimitReduction: 50,  // Reduce rate limit by 50%
			RejectPercentage:   20,  // Reject 20% of requests
			QueueSizeLimit:     200,
			Timeout:            3 * time.Second,
			CircuitBreaker:     true,
		},
		
		CriticalResponse: ResponseStrategy{
			RateLimitReduction: 75,  // Reduce rate limit by 75%
			RejectPercentage:   50,  // Reject 50% of requests
			QueueSizeLimit:     50,
			Timeout:            1 * time.Second,
			CircuitBreaker:     true,
		},
		
		EvaluationInterval: 5 * time.Second,
		SmoothingWindow:    30 * time.Second,
	}
}