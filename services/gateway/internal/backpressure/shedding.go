package backpressure

import (
	"math/rand"
	"sync/atomic"
	"time"
)

// LoadShedder implements intelligent load shedding strategies
type LoadShedder struct {
	// Configuration
	strategy         ShedStrategy
	targetUtilization float64 // Target CPU/memory utilization (0-1)
	
	// Metrics
	cpuUsage    atomic.Uint64 // Stored as percentage * 100
	memoryUsage atomic.Uint64 // Stored as percentage * 100
	queueDepth  atomic.Int64
	
	// Shedding probability
	shedProbability atomic.Uint64 // Stored as probability * 10000
	
	// Stats
	totalRequests   atomic.Int64
	sheddedRequests atomic.Int64
}

// ShedStrategy defines the load shedding strategy
type ShedStrategy int

const (
	// StrategyRandom randomly drops requests based on load
	StrategyRandom ShedStrategy = iota
	
	// StrategyPriority drops low-priority requests first
	StrategyPriority
	
	// StrategyAdaptive uses adaptive algorithms
	StrategyAdaptive
	
	// StrategyTokenBucket uses token bucket for admission control
	StrategyTokenBucket
)

// NewLoadShedder creates a new load shedder
func NewLoadShedder(strategy ShedStrategy, targetUtilization float64) *LoadShedder {
	ls := &LoadShedder{
		strategy:         strategy,
		targetUtilization: targetUtilization,
	}
	
	// Initialize with zero shed probability
	ls.shedProbability.Store(0)
	ls.cpuUsage.Store(0)
	ls.memoryUsage.Store(0)
	
	// Start monitoring goroutine
	go ls.monitor()
	
	return ls
}

// ShouldShed determines if a request should be shed
func (ls *LoadShedder) ShouldShed(priority Priority) bool {
	ls.totalRequests.Add(1)
	
	// Never shed critical requests (health checks, admin)
	if priority == PriorityCritical {
		return false
	}
	
	shed := false
	
	switch ls.strategy {
	case StrategyRandom:
		shed = ls.randomShed()
		
	case StrategyPriority:
		shed = ls.priorityShed(priority)
		
	case StrategyAdaptive:
		shed = ls.adaptiveShed(priority)
		
	case StrategyTokenBucket:
		shed = ls.tokenBucketShed()
		
	default:
		shed = ls.randomShed()
	}
	
	if shed {
		ls.sheddedRequests.Add(1)
	}
	
	return shed
}

// randomShed implements random load shedding
func (ls *LoadShedder) randomShed() bool {
	probability := ls.shedProbability.Load()
	if probability == 0 {
		return false
	}
	
	// Generate random number 0-9999
	r := rand.Intn(10000)
	return uint64(r) < probability
}

// priorityShed implements priority-based shedding
func (ls *LoadShedder) priorityShed(priority Priority) bool {
	probability := ls.shedProbability.Load()
	if probability == 0 {
		return false
	}
	
	// Adjust probability based on priority
	adjustedProb := probability
	switch priority {
	case PriorityHigh:
		adjustedProb = probability / 4 // 25% of normal probability
	case PriorityNormal:
		adjustedProb = probability / 2 // 50% of normal probability
	case PriorityLow:
		adjustedProb = probability * 2 // 200% of normal probability
		if adjustedProb > 10000 {
			adjustedProb = 10000
		}
	}
	
	r := rand.Intn(10000)
	return uint64(r) < adjustedProb
}

// adaptiveShed implements adaptive load shedding using gradient descent
func (ls *LoadShedder) adaptiveShed(priority Priority) bool {
	// Get current system load
	cpu := float64(ls.cpuUsage.Load()) / 100
	mem := float64(ls.memoryUsage.Load()) / 100
	
	// Calculate load factor (0-1)
	loadFactor := (cpu + mem) / 2
	
	// If load is below target, don't shed
	if loadFactor < ls.targetUtilization {
		return false
	}
	
	// Calculate shed probability based on how far over target we are
	overload := (loadFactor - ls.targetUtilization) / (1 - ls.targetUtilization)
	
	// Apply priority multiplier
	switch priority {
	case PriorityHigh:
		overload *= 0.25
	case PriorityNormal:
		overload *= 0.5
	case PriorityLow:
		overload *= 1.5
	}
	
	// Ensure overload is in [0, 1]
	if overload > 1 {
		overload = 1
	}
	if overload < 0 {
		overload = 0
	}
	
	// Random decision based on overload
	return rand.Float64() < overload
}

// tokenBucketShed implements token bucket admission control
func (ls *LoadShedder) tokenBucketShed() bool {
	// Simple implementation - shed if queue is too deep
	queueDepth := ls.queueDepth.Load()
	maxDepth := int64(10000) // Configure based on system
	
	if queueDepth < maxDepth/2 {
		return false
	}
	
	// Linear probability from 50% to 100% of max depth
	shedProb := float64(queueDepth-maxDepth/2) / float64(maxDepth/2)
	return rand.Float64() < shedProb
}

// UpdateMetrics updates system metrics
func (ls *LoadShedder) UpdateMetrics(cpuPercent, memoryPercent float64, queueDepth int64) {
	ls.cpuUsage.Store(uint64(cpuPercent * 100))
	ls.memoryUsage.Store(uint64(memoryPercent * 100))
	ls.queueDepth.Store(queueDepth)
	
	// Update shed probability based on metrics
	ls.updateShedProbability()
}

// updateShedProbability calculates the shed probability based on current metrics
func (ls *LoadShedder) updateShedProbability() {
	cpu := float64(ls.cpuUsage.Load()) / 100
	mem := float64(ls.memoryUsage.Load()) / 100
	
	// Calculate overall system load
	load := (cpu + mem) / 2
	
	// No shedding if below 70% of target
	if load < ls.targetUtilization*0.7 {
		ls.shedProbability.Store(0)
		return
	}
	
	// Linear increase from 0% to 50% shedding as load goes from 70% to 100% of target
	if load < ls.targetUtilization {
		prob := ((load - ls.targetUtilization*0.7) / (ls.targetUtilization * 0.3)) * 0.5
		ls.shedProbability.Store(uint64(prob * 10000))
		return
	}
	
	// Aggressive shedding above target (50% to 90%)
	overloadRatio := (load - ls.targetUtilization) / (1 - ls.targetUtilization)
	prob := 0.5 + (overloadRatio * 0.4)
	if prob > 0.9 {
		prob = 0.9
	}
	
	ls.shedProbability.Store(uint64(prob * 10000))
}

// monitor periodically updates system metrics
func (ls *LoadShedder) monitor() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for range ticker.C {
		// In production, get actual CPU and memory usage
		// For now, we rely on external updates via UpdateMetrics
		ls.updateShedProbability()
	}
}

// GetStats returns load shedding statistics
func (ls *LoadShedder) GetStats() map[string]int64 {
	total := ls.totalRequests.Load()
	shed := ls.sheddedRequests.Load()
	
	shedRate := int64(0)
	if total > 0 {
		shedRate = (shed * 100) / total
	}
	
	return map[string]int64{
		"total_requests":   total,
		"shed_requests":    shed,
		"shed_rate":        shedRate,
		"shed_probability": int64(ls.shedProbability.Load()),
		"cpu_usage":        int64(ls.cpuUsage.Load()),
		"memory_usage":     int64(ls.memoryUsage.Load()),
		"queue_depth":      ls.queueDepth.Load(),
	}
}