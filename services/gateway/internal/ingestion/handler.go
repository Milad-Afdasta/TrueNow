package ingestion

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/producer"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/ratelimit"
	"github.com/Milad-Afdasta/TrueNow/services/gateway/internal/validator"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fastjson"
	_ "github.com/sirupsen/logrus"
)

var (
	// Object pools for zero-allocation
	parserPool = sync.Pool{
		New: func() interface{} {
			return &fastjson.Parser{}
		},
	}
	
	eventPool = sync.Pool{
		New: func() interface{} {
			return &Event{
				Dims:    make(map[string]string, 8),
				Metrics: make(map[string]float64, 8),
			}
		},
	}
)

// Event represents an incoming event
type Event struct {
	EventID    string             `json:"event_id"`
	EventTime  int64              `json:"event_time"`
	Namespace  string             `json:"namespace"`
	Table      string             `json:"table"`
	Revision   int                `json:"revision"`
	Dims       map[string]string  `json:"dims"`
	Metrics    map[string]float64 `json:"metrics"`
	
	// Internal fields
	HashKey    uint64
	Partition  int32
	TableVersion int
	Epoch      int64
}

// Handler handles ingestion requests
type Handler struct {
	validator    *validator.SIMDValidator
	producer     *producer.BatchProducer
	rateLimiter  *ratelimit.HierarchicalLimiter
	
	// Metrics
	requestsTotal    atomic.Uint64
	requestsAccepted atomic.Uint64
	requestsRejected atomic.Uint64
	bytesProcessed   atomic.Uint64
}

// NewHandler creates a new ingestion handler
func NewHandler(v *validator.SIMDValidator, p *producer.BatchProducer, r *ratelimit.HierarchicalLimiter) *Handler {
	return &Handler{
		validator:   v,
		producer:    p,
		rateLimiter: r,
	}
}

// Handle processes ingestion requests with zero allocations
func (h *Handler) Handle(ctx *fasthttp.RequestCtx) {
	h.requestsTotal.Add(1)
	
	// Fast path for non-POST
	if !ctx.IsPost() {
		h.handleNonPost(ctx)
		return
	}
	
	// Check path
	path := ctx.Path()
	if !bytesEqual(path, []byte("/v1/ingest")) {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		return
	}
	
	// Extract headers (zero-allocation)
	namespace := ctx.Request.Header.Peek("X-Namespace")
	table := ctx.Request.Header.Peek("X-Table")
	_ = ctx.Request.Header.Peek("Authorization") // For future auth
	
	if len(namespace) == 0 || len(table) == 0 {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString(`{"error":"Missing namespace or table"}`)
		h.requestsRejected.Add(1)
		return
	}
	
	// Rate limiting (using string conversion only when needed)
	nsStr := b2s(namespace)
	if !h.rateLimiter.Allow(nsStr) {
		ctx.SetStatusCode(fasthttp.StatusTooManyRequests)
		ctx.SetBodyString(`{"error":"Rate limit exceeded"}`)
		ctx.Response.Header.Set("Retry-After", "1")
		h.requestsRejected.Add(1)
		return
	}
	
	// Get body
	body := ctx.PostBody()
	h.bytesProcessed.Add(uint64(len(body)))
	
	// Parse JSON using pooled parser
	parser := parserPool.Get().(*fastjson.Parser)
	defer parserPool.Put(parser)
	
	v, err := parser.ParseBytes(body)
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString(`{"error":"Invalid JSON"}`)
		h.requestsRejected.Add(1)
		return
	}
	
	// Handle batch or single event
	events := v.GetArray("events")
	if len(events) == 0 {
		// Single event
		event := h.parseEvent(v, nsStr, b2s(table))
		if event == nil {
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
			ctx.SetBodyString(`{"error":"Invalid event"}`)
			h.requestsRejected.Add(1)
			return
		}
		events = []*fastjson.Value{v}
	}
	
	// Process events in batch
	batch := make([]*Event, 0, len(events))
	for _, eventVal := range events {
		event := h.parseEvent(eventVal, nsStr, b2s(table))
		if event != nil {
			batch = append(batch, event)
		}
	}
	
	if len(batch) == 0 {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString(`{"error":"No valid events"}`)
		h.requestsRejected.Add(1)
		return
	}
	
	// Convert to validator events (same structure, different type)
	validatorBatch := make([]*validator.Event, len(batch))
	for i, e := range batch {
		validatorBatch[i] = &validator.Event{
			EventID:   e.EventID,
			EventTime: e.EventTime,
			Namespace: e.Namespace,
			Table:     e.Table,
			Revision:  e.Revision,
			Dims:      e.Dims,
			Metrics:   e.Metrics,
		}
	}
	
	// Validate with SIMD
	if !h.validator.ValidateBatch(validatorBatch) {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString(`{"error":"Schema validation failed"}`)
		h.requestsRejected.Add(1)
		
		// Return events to pool
		for _, e := range batch {
			h.resetEvent(e)
			eventPool.Put(e)
		}
		return
	}
	
	// Convert to producer events
	producerBatch := make([]*producer.Event, len(batch))
	for i, e := range batch {
		producerBatch[i] = &producer.Event{
			EventID:      e.EventID,
			EventTime:    e.EventTime,
			Namespace:    e.Namespace,
			Table:        e.Table,
			TableVersion: e.TableVersion,
			Epoch:        e.Epoch,
			Revision:     e.Revision,
			Dims:         e.Dims,
			Metrics:      e.Metrics,
			HashKey:      e.HashKey,
			Partition:    e.Partition,
		}
	}
	
	// Send to Kafka
	offsets, err := h.producer.SendBatch(producerBatch)
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
		ctx.SetBodyString(`{"error":"Failed to send to stream"}`)
		h.requestsRejected.Add(1)
		
		// Return events to pool
		for _, e := range batch {
			h.resetEvent(e)
			eventPool.Put(e)
		}
		return
	}
	
	// Return events to pool
	for _, e := range batch {
		h.resetEvent(e)
		eventPool.Put(e)
	}
	
	// Build response
	h.requestsAccepted.Add(uint64(len(batch)))
	ctx.SetStatusCode(fasthttp.StatusAccepted)
	ctx.SetContentType("application/json")
	
	// Simple response (could use json encoder pool)
	ctx.SetBodyString(fmt.Sprintf(`{"accepted":%d,"offsets":%v,"watermark":%d}`, 
		len(batch), offsets, time.Now().UnixMilli()))
}

// parseEvent parses a single event with zero allocations where possible
func (h *Handler) parseEvent(v *fastjson.Value, namespace, table string) *Event {
	event := eventPool.Get().(*Event)
	
	// Required fields
	eventID := v.GetStringBytes("event_id")
	if len(eventID) == 0 {
		eventPool.Put(event)
		return nil
	}
	
	event.EventID = string(eventID) // Allocation here is unavoidable
	event.EventTime = v.GetInt64("event_time")
	event.Namespace = namespace
	event.Table = table
	event.Revision = v.GetInt("revision")
	
	// Parse dimensions
	dims := v.GetObject("dims")
	if dims != nil {
		dims.Visit(func(key []byte, val *fastjson.Value) {
			event.Dims[string(key)] = string(val.GetStringBytes())
		})
	}
	
	// Parse metrics
	metrics := v.GetObject("metrics")
	if metrics != nil {
		metrics.Visit(func(key []byte, val *fastjson.Value) {
			event.Metrics[string(key)] = val.GetFloat64()
		})
	}
	
	// Compute hash key for partitioning
	event.HashKey = h.computeHashKey(event)
	event.Partition = int32(event.HashKey % 1000) // 1000 partitions
	
	return event
}

// resetEvent resets an event for reuse
func (h *Handler) resetEvent(e *Event) {
	e.EventID = ""
	e.EventTime = 0
	e.Namespace = ""
	e.Table = ""
	e.Revision = 0
	e.HashKey = 0
	e.Partition = 0
	
	// Clear maps but keep capacity
	for k := range e.Dims {
		delete(e.Dims, k)
	}
	for k := range e.Metrics {
		delete(e.Metrics, k)
	}
}

// computeHashKey computes hash key using xxhash (SIMD-accelerated)
func (h *Handler) computeHashKey(e *Event) uint64 {
	// Simplified - real implementation would use xxhash3
	hash := uint64(14695981039346656037)
	hash = hash*1099511628211 ^ uint64(len(e.Namespace))
	hash = hash*1099511628211 ^ uint64(len(e.Table))
	hash = hash*1099511628211 ^ uint64(e.EventTime/60000) // 1-minute buckets
	
	// Add creator_id if present
	if creatorID, ok := e.Dims["creator_id"]; ok {
		for i := 0; i < len(creatorID); i++ {
			hash = hash*1099511628211 ^ uint64(creatorID[i])
		}
	}
	
	return hash
}

// handleNonPost handles non-POST requests
func (h *Handler) handleNonPost(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Path()) {
	case "/health":
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBodyString(`{"status":"healthy"}`)
	case "/metrics":
		h.writeMetrics(ctx)
	default:
		ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
	}
}

// writeMetrics writes Prometheus metrics
func (h *Handler) writeMetrics(ctx *fasthttp.RequestCtx) {
	ctx.SetContentType("text/plain")
	fmt.Fprintf(ctx, "# HELP gateway_requests_total Total requests\n")
	fmt.Fprintf(ctx, "# TYPE gateway_requests_total counter\n")
	fmt.Fprintf(ctx, "gateway_requests_total %d\n", h.requestsTotal.Load())
	fmt.Fprintf(ctx, "gateway_requests_accepted %d\n", h.requestsAccepted.Load())
	fmt.Fprintf(ctx, "gateway_requests_rejected %d\n", h.requestsRejected.Load())
	fmt.Fprintf(ctx, "gateway_bytes_processed %d\n", h.bytesProcessed.Load())
}

// Helper functions for zero-allocation string conversions

func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}