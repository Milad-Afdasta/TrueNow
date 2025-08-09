package circuit

import "errors"

var (
	// ErrCircuitOpen is returned when the circuit breaker is open
	ErrCircuitOpen = errors.New("circuit breaker is open")
	
	// ErrQueueFull is returned when the request queue is full
	ErrQueueFull = errors.New("request queue is full")
	
	// ErrTimeout is returned when a queued request times out
	ErrTimeout = errors.New("request timeout")
)