package circuit

import (
	"sync"
	"sync/atomic"
	"time"
)

// State represents the circuit breaker state
type State int32

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

// CircuitBreaker implements the circuit breaker pattern for backpressure management
type CircuitBreaker struct {
	// Configuration
	maxFailures      int64         // Maximum failures before opening
	maxRequests      int64         // Max requests in half-open state
	timeout          time.Duration // Time before attempting recovery
	backoffMultiplier float64      // Exponential backoff multiplier
	
	// State
	state           atomic.Int32
	failures        atomic.Int64
	successes       atomic.Int64
	lastFailureTime atomic.Int64
	halfOpenRequests atomic.Int64
	
	// Metrics
	totalRequests   atomic.Int64
	totalFailures   atomic.Int64
	totalSuccesses  atomic.Int64
	rejectedRequests atomic.Int64
	
	mu sync.RWMutex
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(maxFailures int64, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:      maxFailures,
		maxRequests:      maxFailures / 2, // Allow half the failures for testing
		timeout:          timeout,
		backoffMultiplier: 2.0,
	}
}

// Call executes the function if the circuit allows it
func (cb *CircuitBreaker) Call(fn func() error) error {
	if !cb.AllowRequest() {
		cb.rejectedRequests.Add(1)
		return ErrCircuitOpen
	}
	
	cb.totalRequests.Add(1)
	
	err := fn()
	if err != nil {
		cb.RecordFailure()
		return err
	}
	
	cb.RecordSuccess()
	return nil
}

// AllowRequest checks if a request is allowed
func (cb *CircuitBreaker) AllowRequest() bool {
	state := State(cb.state.Load())
	
	switch state {
	case StateClosed:
		return true
		
	case StateOpen:
		// Check if we should transition to half-open
		lastFailure := cb.lastFailureTime.Load()
		if time.Since(time.Unix(0, lastFailure)) > cb.timeout {
			cb.transitionTo(StateHalfOpen)
			return true
		}
		return false
		
	case StateHalfOpen:
		// Allow limited requests for testing
		current := cb.halfOpenRequests.Add(1)
		return current <= cb.maxRequests
		
	default:
		return false
	}
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.totalSuccesses.Add(1)
	state := State(cb.state.Load())
	
	switch state {
	case StateHalfOpen:
		successes := cb.successes.Add(1)
		// If we've had enough successes, close the circuit
		if successes >= cb.maxRequests/2 {
			cb.transitionTo(StateClosed)
		}
		
	case StateClosed:
		// Reset failure count on success
		cb.failures.Store(0)
	}
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.totalFailures.Add(1)
	cb.lastFailureTime.Store(time.Now().UnixNano())
	
	state := State(cb.state.Load())
	
	switch state {
	case StateClosed:
		failures := cb.failures.Add(1)
		if failures >= cb.maxFailures {
			cb.transitionTo(StateOpen)
		}
		
	case StateHalfOpen:
		// Any failure in half-open state reopens the circuit
		cb.transitionTo(StateOpen)
		// Apply exponential backoff
		cb.timeout = time.Duration(float64(cb.timeout) * cb.backoffMultiplier)
		if cb.timeout > 30*time.Second {
			cb.timeout = 30 * time.Second // Cap at 30 seconds
		}
	}
}

// transitionTo changes the circuit breaker state
func (cb *CircuitBreaker) transitionTo(newState State) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	oldState := State(cb.state.Load())
	
	// Reset counters based on transition
	switch newState {
	case StateClosed:
		cb.failures.Store(0)
		cb.successes.Store(0)
		cb.halfOpenRequests.Store(0)
		cb.timeout = 1 * time.Second // Reset timeout
		
	case StateOpen:
		cb.failures.Store(0)
		cb.successes.Store(0)
		
	case StateHalfOpen:
		cb.halfOpenRequests.Store(0)
		cb.successes.Store(0)
	}
	
	cb.state.Store(int32(newState))
	
	// Log state transition
	if oldState != newState {
		// In production, emit metrics here
	}
}

// GetState returns the current state
func (cb *CircuitBreaker) GetState() State {
	return State(cb.state.Load())
}

// GetMetrics returns circuit breaker metrics
func (cb *CircuitBreaker) GetMetrics() map[string]int64 {
	return map[string]int64{
		"total_requests":   cb.totalRequests.Load(),
		"total_failures":   cb.totalFailures.Load(),
		"total_successes":  cb.totalSuccesses.Load(),
		"rejected_requests": cb.rejectedRequests.Load(),
		"current_failures": cb.failures.Load(),
		"state":           int64(cb.state.Load()),
	}
}

// Reset resets the circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.transitionTo(StateClosed)
	cb.totalRequests.Store(0)
	cb.totalFailures.Store(0)
	cb.totalSuccesses.Store(0)
	cb.rejectedRequests.Store(0)
}