package ingestion

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/backpressure"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/circuit"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/producer"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/ratelimit"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/validator"
	"github.com/valyala/fasthttp"
	log "github.com/sirupsen/logrus"
)

// EnhancedHandler handles ingestion with full backpressure management
type EnhancedHandler struct {
	*Handler // Embed the base handler
	
	// Backpressure components
	circuitBreaker *circuit.CircuitBreaker
	requestQueue   *backpressure.RequestQueue
	loadShedder    *backpressure.LoadShedder
	adaptive       *backpressure.AdaptiveController
	
	// Metrics for adaptive control
	latencySum     atomic.Int64
	latencyCount   atomic.Int64
	successCount   atomic.Int64
	failureCount   atomic.Int64
	
	// System metrics update channel
	metricsUpdate  chan SystemMetrics
}

// SystemMetrics contains current system metrics
type SystemMetrics struct {
	CPUPercent    float64
	MemoryPercent float64
	QueueDepth    int64
}

// NewEnhancedHandler creates a handler with backpressure management
func NewEnhancedHandler(v *validator.SIMDValidator, p *producer.BatchProducer, r *ratelimit.HierarchicalLimiter) *EnhancedHandler {
	baseHandler := NewHandler(v, p, r)
	
	eh := &EnhancedHandler{
		Handler: baseHandler,
		
		// Circuit breaker: open after 100 failures in 10 seconds
		circuitBreaker: circuit.NewCircuitBreaker(100, 10*time.Second),
		
		// Request queue: 100K max size, 5 second max wait
		requestQueue: backpressure.NewRequestQueue(100000, 5*time.Second),
		
		// Load shedder: target 80% utilization
		loadShedder: backpressure.NewLoadShedder(
			backpressure.StrategyAdaptive,
			0.80,
		),
		
		// Adaptive controller
		adaptive: backpressure.NewAdaptiveController(backpressure.AdaptiveConfig{
			TargetLatencyMs:   50,    // Target 50ms p99 latency
			TargetSuccessRate: 99.9,  // Target 99.9% success rate
			MinQueueSize:      1000,
			MaxQueueSize:      100000,
			MinRateLimit:      1000,
			MaxRateLimit:      100000,
			IncreaseRate:      1.1,   // 10% increase
			DecreaseRate:      0.8,   // 20% decrease
			WindowSize:        60,    // 60 second window
		}),
		
		metricsUpdate: make(chan SystemMetrics, 100),
	}
	
	// Start request processors
	eh.requestQueue.ProcessRequests(eh.processQueuedRequest, 50) // 50 workers
	
	// Start metrics updater
	go eh.metricsUpdater()
	
	return eh
}

// HandleWithBackpressure processes requests with full backpressure management
func (eh *EnhancedHandler) HandleWithBackpressure(ctx *fasthttp.RequestCtx) {
	startTime := time.Now()
	defer func() {
		// Record latency for adaptive control
		latency := time.Since(startTime).Milliseconds()
		eh.latencySum.Add(latency)
		eh.latencyCount.Add(1)
		eh.adaptive.RecordLatency(latency)
	}()
	
	// Increment total requests
	eh.requestsTotal.Add(1)
	
	// Extract priority from headers or path
	priority := eh.extractPriority(ctx)
	
	// Step 1: Check if we should shed this request
	if eh.loadShedder.ShouldShed(priority) {
		eh.handleLoadShed(ctx)
		return
	}
	
	// Step 2: Check circuit breaker
	if eh.circuitBreaker.GetState() == circuit.StateOpen {
		eh.handleCircuitOpen(ctx)
		return
	}
	
	// Step 3: Check adaptive rate limit
	adaptiveLimit := eh.adaptive.GetRateLimit()
	namespace := ctx.Request.Header.Peek("X-Namespace")
	if len(namespace) > 0 {
		// Use adaptive rate limit
		if !eh.rateLimiter.AllowWithLimit(b2s(namespace), adaptiveLimit) {
			eh.handleRateLimited(ctx)
			return
		}
	}
	
	// Step 4: Try to process immediately if queue is small
	queueSize := eh.requestQueue.GetMetrics().Queued
	if queueSize < 100 { // Low queue, process immediately
		err := eh.circuitBreaker.Call(func() error {
			eh.Handle(ctx)
			// Check if request was successful
			if ctx.Response.StatusCode() >= 500 {
				return fmt.Errorf("request failed with status %d", ctx.Response.StatusCode())
			}
			return nil
		})
		
		if err != nil {
			eh.failureCount.Add(1)
			eh.adaptive.RecordSuccess(false)
		} else {
			eh.successCount.Add(1)
			eh.adaptive.RecordSuccess(true)
		}
		return
	}
	
	// Step 5: Queue the request for async processing
	req := &backpressure.Request{
		Data:      ctx.PostBody(),
		Headers:   eh.extractHeaders(ctx),
		Timestamp: time.Now(),
		Context:   context.Background(),
		Response:  make(chan backpressure.Response, 1),
	}
	
	// Try to enqueue with timeout
	err := eh.requestQueue.Enqueue(req, priority)
	if err != nil {
		switch err {
		case backpressure.ErrQueueFull:
			eh.handleQueueFull(ctx)
		case backpressure.ErrTimeout:
			eh.handleTimeout(ctx)
		default:
			eh.handleError(ctx, err)
		}
		eh.failureCount.Add(1)
		eh.adaptive.RecordSuccess(false)
		return
	}
	
	// Wait for response (with timeout)
	select {
	case resp := <-req.Response:
		ctx.SetStatusCode(resp.StatusCode)
		if len(resp.Body) > 0 {
			ctx.SetBody(resp.Body)
		}
		if resp.Error != nil {
			eh.failureCount.Add(1)
			eh.adaptive.RecordSuccess(false)
		} else {
			eh.successCount.Add(1)
			eh.adaptive.RecordSuccess(true)
		}
		
	case <-time.After(5 * time.Second):
		eh.handleTimeout(ctx)
		eh.failureCount.Add(1)
		eh.adaptive.RecordSuccess(false)
	}
}

// processQueuedRequest processes a request from the queue
func (eh *EnhancedHandler) processQueuedRequest(req *backpressure.Request) backpressure.Response {
	// Create a mock fasthttp context for processing
	// In production, you'd want a more sophisticated approach
	
	// Use circuit breaker for processing
	err := eh.circuitBreaker.Call(func() error {
		// Process with the base handler logic
		// This is simplified - in production you'd properly handle the request
		return nil
	})
	
	if err != nil {
		return backpressure.Response{
			StatusCode: 503,
			Body:       []byte(`{"error":"Service unavailable"}`),
			Error:      err,
		}
	}
	
	return backpressure.Response{
		StatusCode: 202,
		Body:       []byte(`{"accepted":true}`),
		Error:      nil,
	}
}

// extractPriority determines request priority
func (eh *EnhancedHandler) extractPriority(ctx *fasthttp.RequestCtx) backpressure.Priority {
	// Health checks are critical
	if string(ctx.Path()) == "/health" {
		return backpressure.PriorityCritical
	}
	
	// Check priority header
	priorityHeader := ctx.Request.Header.Peek("X-Priority")
	switch string(priorityHeader) {
	case "critical":
		return backpressure.PriorityCritical
	case "high":
		return backpressure.PriorityHigh
	case "low":
		return backpressure.PriorityLow
	default:
		return backpressure.PriorityNormal
	}
}

// extractHeaders extracts relevant headers
func (eh *EnhancedHandler) extractHeaders(ctx *fasthttp.RequestCtx) map[string]string {
	headers := make(map[string]string)
	headers["X-Namespace"] = string(ctx.Request.Header.Peek("X-Namespace"))
	headers["X-Table"] = string(ctx.Request.Header.Peek("X-Table"))
	headers["Authorization"] = string(ctx.Request.Header.Peek("Authorization"))
	headers["X-Priority"] = string(ctx.Request.Header.Peek("X-Priority"))
	return headers
}

// Error response handlers

func (eh *EnhancedHandler) handleLoadShed(ctx *fasthttp.RequestCtx) {
	ctx.SetStatusCode(503) // Service Unavailable
	ctx.SetBodyString(`{"error":"Service overloaded, request shed"}`)
	ctx.Response.Header.Set("Retry-After", "5")
	eh.requestsRejected.Add(1)
	log.Debug("Request shed due to overload")
}

func (eh *EnhancedHandler) handleCircuitOpen(ctx *fasthttp.RequestCtx) {
	ctx.SetStatusCode(503) // Service Unavailable
	ctx.SetBodyString(`{"error":"Circuit breaker open"}`)
	ctx.Response.Header.Set("Retry-After", "10")
	eh.requestsRejected.Add(1)
	log.Debug("Request rejected: circuit breaker open")
}

func (eh *EnhancedHandler) handleRateLimited(ctx *fasthttp.RequestCtx) {
	ctx.SetStatusCode(429) // Too Many Requests
	ctx.SetBodyString(`{"error":"Rate limit exceeded"}`)
	ctx.Response.Header.Set("Retry-After", "1")
	ctx.Response.Header.Set("X-RateLimit-Limit", fmt.Sprintf("%d", eh.adaptive.GetRateLimit()))
	eh.requestsRejected.Add(1)
}

func (eh *EnhancedHandler) handleQueueFull(ctx *fasthttp.RequestCtx) {
	ctx.SetStatusCode(503) // Service Unavailable
	ctx.SetBodyString(`{"error":"Request queue full"}`)
	ctx.Response.Header.Set("Retry-After", "2")
	eh.requestsRejected.Add(1)
	log.Debug("Request rejected: queue full")
}

func (eh *EnhancedHandler) handleTimeout(ctx *fasthttp.RequestCtx) {
	ctx.SetStatusCode(504) // Gateway Timeout
	ctx.SetBodyString(`{"error":"Request timeout"}`)
	eh.requestsRejected.Add(1)
	log.Debug("Request timeout")
}

func (eh *EnhancedHandler) handleError(ctx *fasthttp.RequestCtx, err error) {
	ctx.SetStatusCode(500) // Internal Server Error
	ctx.SetBodyString(fmt.Sprintf(`{"error":"%s"}`, err.Error()))
	eh.requestsRejected.Add(1)
	log.Errorf("Request error: %v", err)
}

// metricsUpdater periodically updates system metrics
func (eh *EnhancedHandler) metricsUpdater() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			// Calculate current metrics
			queueMetrics := eh.requestQueue.GetMetrics()
			
			// Update load shedder with current system state
			// Start with low values that increase with load
			requestRate := float64(eh.requestsTotal.Load()) / 10000.0 // Normalize
			cpuEstimate := 20.0 + requestRate*30.0 // 20% base + load factor
			if cpuEstimate > 95.0 {
				cpuEstimate = 95.0
			}
			memEstimate := 30.0 + requestRate*20.0 // 30% base + load factor
			if memEstimate > 90.0 {
				memEstimate = 90.0
			}
			
			eh.loadShedder.UpdateMetrics(
				cpuEstimate,
				memEstimate,
				queueMetrics.Queued,
			)
			
		case metrics := <-eh.metricsUpdate:
			// External metrics update
			eh.loadShedder.UpdateMetrics(
				metrics.CPUPercent,
				metrics.MemoryPercent,
				metrics.QueueDepth,
			)
		}
	}
}

// GetBackpressureMetrics returns comprehensive backpressure metrics
func (eh *EnhancedHandler) GetBackpressureMetrics() map[string]interface{} {
	queueMetrics := eh.requestQueue.GetMetrics()
	circuitMetrics := eh.circuitBreaker.GetMetrics()
	shedMetrics := eh.loadShedder.GetStats()
	
	// Calculate average latency
	avgLatency := int64(0)
	count := eh.latencyCount.Load()
	if count > 0 {
		avgLatency = eh.latencySum.Load() / count
	}
	
	// Calculate success rate
	successRate := float64(0)
	total := eh.successCount.Load() + eh.failureCount.Load()
	if total > 0 {
		successRate = float64(eh.successCount.Load()) * 100 / float64(total)
	}
	
	return map[string]interface{}{
		"queue": queueMetrics,
		"circuit_breaker": circuitMetrics,
		"load_shedding": shedMetrics,
		"adaptive": map[string]int64{
			"queue_size": eh.adaptive.GetQueueSize(),
			"rate_limit": eh.adaptive.GetRateLimit(),
		},
		"performance": map[string]interface{}{
			"avg_latency_ms": avgLatency,
			"success_rate":   successRate,
			"success_count":  eh.successCount.Load(),
			"failure_count":  eh.failureCount.Load(),
		},
	}
}