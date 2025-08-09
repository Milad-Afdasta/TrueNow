package backpressure

import (
	"sync"
	"sync/atomic"
	"time"
)

// AdaptiveController dynamically adjusts backpressure based on system performance
type AdaptiveController struct {
	config AdaptiveConfig
	
	// Current limits
	currentQueueSize atomic.Int64
	currentRateLimit atomic.Int64
	
	// Performance metrics (ring buffer)
	latencies     []int64
	successRates  []float64
	index         int
	mu            sync.RWMutex
	
	// Control loop
	ticker *time.Ticker
	done   chan bool
}

// NewAdaptiveController creates a new adaptive backpressure controller
func NewAdaptiveController(config AdaptiveConfig) *AdaptiveController {
	ac := &AdaptiveController{
		config:       config,
		latencies:    make([]int64, config.WindowSize),
		successRates: make([]float64, config.WindowSize),
		ticker:       time.NewTicker(1 * time.Second),
		done:         make(chan bool),
	}
	
	// Set initial limits (midpoint)
	ac.currentQueueSize.Store((config.MinQueueSize + config.MaxQueueSize) / 2)
	ac.currentRateLimit.Store((config.MinRateLimit + config.MaxRateLimit) / 2)
	
	// Start control loop
	go ac.controlLoop()
	
	return ac
}

// RecordLatency records a request latency
func (ac *AdaptiveController) RecordLatency(latencyMs int64) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	
	ac.latencies[ac.index] = latencyMs
}

// RecordSuccess records success/failure
func (ac *AdaptiveController) RecordSuccess(success bool) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	
	if success {
		ac.successRates[ac.index] += 1
	}
}

// controlLoop runs the adaptive control algorithm
func (ac *AdaptiveController) controlLoop() {
	for {
		select {
		case <-ac.ticker.C:
			ac.adjust()
		case <-ac.done:
			return
		}
	}
}

// adjust adapts the limits based on current performance
func (ac *AdaptiveController) adjust() {
	ac.mu.Lock()
	
	// Move to next window slot
	ac.index = (ac.index + 1) % ac.config.WindowSize
	
	// Calculate average latency and success rate
	var totalLatency int64
	var totalSuccess float64
	var count int
	
	for i := 0; i < ac.config.WindowSize; i++ {
		if ac.latencies[i] > 0 {
			totalLatency += ac.latencies[i]
			totalSuccess += ac.successRates[i]
			count++
		}
	}
	
	// Reset current slot
	ac.latencies[ac.index] = 0
	ac.successRates[ac.index] = 0
	
	ac.mu.Unlock()
	
	if count == 0 {
		return // No data yet
	}
	
	avgLatency := totalLatency / int64(count)
	avgSuccessRate := (totalSuccess / float64(count)) * 100
	
	// Adjust based on performance
	currentQueueSize := ac.currentQueueSize.Load()
	currentRateLimit := ac.currentRateLimit.Load()
	
	// If latency is too high or success rate too low, decrease limits
	if avgLatency > ac.config.TargetLatencyMs || avgSuccessRate < ac.config.TargetSuccessRate {
		// Decrease queue size
		newQueueSize := int64(float64(currentQueueSize) * ac.config.DecreaseRate)
		if newQueueSize < ac.config.MinQueueSize {
			newQueueSize = ac.config.MinQueueSize
		}
		ac.currentQueueSize.Store(newQueueSize)
		
		// Decrease rate limit
		newRateLimit := int64(float64(currentRateLimit) * ac.config.DecreaseRate)
		if newRateLimit < ac.config.MinRateLimit {
			newRateLimit = ac.config.MinRateLimit
		}
		ac.currentRateLimit.Store(newRateLimit)
		
	} else if avgLatency < ac.config.TargetLatencyMs*8/10 && avgSuccessRate > ac.config.TargetSuccessRate {
		// If we're well below targets, increase limits
		
		// Increase queue size
		newQueueSize := int64(float64(currentQueueSize) * ac.config.IncreaseRate)
		if newQueueSize > ac.config.MaxQueueSize {
			newQueueSize = ac.config.MaxQueueSize
		}
		ac.currentQueueSize.Store(newQueueSize)
		
		// Increase rate limit
		newRateLimit := int64(float64(currentRateLimit) * ac.config.IncreaseRate)
		if newRateLimit > ac.config.MaxRateLimit {
			newRateLimit = ac.config.MaxRateLimit
		}
		ac.currentRateLimit.Store(newRateLimit)
	}
}

// GetQueueSize returns the current adaptive queue size
func (ac *AdaptiveController) GetQueueSize() int64 {
	return ac.currentQueueSize.Load()
}

// GetRateLimit returns the current adaptive rate limit
func (ac *AdaptiveController) GetRateLimit() int64 {
	return ac.currentRateLimit.Load()
}

// Stop stops the adaptive controller
func (ac *AdaptiveController) Stop() {
	ac.ticker.Stop()
	close(ac.done)
}