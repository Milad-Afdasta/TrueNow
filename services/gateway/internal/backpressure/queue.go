package backpressure

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Request represents a queued request
type Request struct {
	Data      []byte
	Headers   map[string]string
	Timestamp time.Time
	Context   context.Context
	Response  chan Response
}

// Response represents the response to a queued request
type Response struct {
	StatusCode int
	Body       []byte
	Error      error
}

// RequestQueue implements a bounded queue with timeout and priority support
type RequestQueue struct {
	// Configuration
	maxSize        int64
	maxWaitTime    time.Duration
	maxProcessTime time.Duration
	
	// Channels
	highPriority   chan *Request
	normalPriority chan *Request
	lowPriority    chan *Request
	
	// Metrics
	queued         atomic.Int64
	processed      atomic.Int64
	dropped        atomic.Int64
	timedOut       atomic.Int64
	
	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRequestQueue creates a new request queue with backpressure management
func NewRequestQueue(maxSize int64, maxWaitTime time.Duration) *RequestQueue {
	ctx, cancel := context.WithCancel(context.Background())
	
	// Allocate queue sizes based on priority
	// High: 20%, Normal: 60%, Low: 20%
	highSize := maxSize / 5
	normalSize := (maxSize * 3) / 5
	lowSize := maxSize / 5
	
	return &RequestQueue{
		maxSize:        maxSize,
		maxWaitTime:    maxWaitTime,
		maxProcessTime: 5 * time.Second,
		highPriority:   make(chan *Request, highSize),
		normalPriority: make(chan *Request, normalSize),
		lowPriority:    make(chan *Request, lowSize),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Enqueue adds a request to the queue with priority
func (q *RequestQueue) Enqueue(req *Request, priority Priority) error {
	// Check if we're at capacity
	if q.queued.Load() >= q.maxSize {
		q.dropped.Add(1)
		return ErrQueueFull
	}
	
	// Set timeout context
	ctx, cancel := context.WithTimeout(req.Context, q.maxWaitTime)
	req.Context = ctx
	defer cancel()
	
	// Increment queued counter
	q.queued.Add(1)
	defer func() {
		if ctx.Err() != nil {
			q.timedOut.Add(1)
		}
	}()
	
	// Try to enqueue based on priority
	select {
	case <-ctx.Done():
		q.queued.Add(-1)
		return ErrTimeout
		
	default:
		switch priority {
		case PriorityHigh:
			select {
			case q.highPriority <- req:
				return nil
			case <-ctx.Done():
				q.queued.Add(-1)
				return ErrTimeout
			default:
				// Fallback to normal priority
				select {
				case q.normalPriority <- req:
					return nil
				case <-ctx.Done():
					q.queued.Add(-1)
					return ErrTimeout
				}
			}
			
		case PriorityNormal:
			select {
			case q.normalPriority <- req:
				return nil
			case <-ctx.Done():
				q.queued.Add(-1)
				return ErrTimeout
			default:
				// Try low priority as fallback
				select {
				case q.lowPriority <- req:
					return nil
				case <-ctx.Done():
					q.queued.Add(-1)
					return ErrTimeout
				}
			}
			
		case PriorityLow:
			select {
			case q.lowPriority <- req:
				return nil
			case <-ctx.Done():
				q.queued.Add(-1)
				return ErrTimeout
			default:
				q.dropped.Add(1)
				q.queued.Add(-1)
				return ErrQueueFull
			}
			
		default:
			q.queued.Add(-1)
			return ErrInvalidPriority
		}
	}
}

// ProcessRequests starts processing queued requests
func (q *RequestQueue) ProcessRequests(processor func(*Request) Response, workers int) {
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go q.worker(processor)
	}
}

// worker processes requests from the queues
func (q *RequestQueue) worker(processor func(*Request) Response) {
	defer q.wg.Done()
	
	for {
		select {
		case <-q.ctx.Done():
			return
			
		// Process high priority first
		case req := <-q.highPriority:
			q.processRequest(req, processor)
			
		default:
			select {
			case <-q.ctx.Done():
				return
				
			// Then normal priority
			case req := <-q.normalPriority:
				q.processRequest(req, processor)
				
			default:
				select {
				case <-q.ctx.Done():
					return
					
				// Finally low priority
				case req := <-q.lowPriority:
					q.processRequest(req, processor)
					
				// No requests available, wait a bit
				case <-time.After(10 * time.Millisecond):
					continue
				}
			}
		}
	}
}

// processRequest handles a single request
func (q *RequestQueue) processRequest(req *Request, processor func(*Request) Response) {
	q.queued.Add(-1)
	
	// Check if request has already timed out
	select {
	case <-req.Context.Done():
		q.timedOut.Add(1)
		req.Response <- Response{
			StatusCode: 504, // Gateway Timeout
			Error:      ErrTimeout,
		}
		return
	default:
	}
	
	// Process with timeout
	done := make(chan Response, 1)
	go func() {
		done <- processor(req)
	}()
	
	select {
	case resp := <-done:
		q.processed.Add(1)
		req.Response <- resp
		
	case <-time.After(q.maxProcessTime):
		q.timedOut.Add(1)
		req.Response <- Response{
			StatusCode: 504,
			Error:      ErrTimeout,
		}
		
	case <-req.Context.Done():
		q.timedOut.Add(1)
		req.Response <- Response{
			StatusCode: 504,
			Error:      ErrTimeout,
		}
	}
}

// GetMetrics returns queue metrics
func (q *RequestQueue) GetMetrics() QueueMetrics {
	return QueueMetrics{
		Queued:    q.queued.Load(),
		Processed: q.processed.Load(),
		Dropped:   q.dropped.Load(),
		TimedOut:  q.timedOut.Load(),
		QueueSizes: map[string]int{
			"high":   len(q.highPriority),
			"normal": len(q.normalPriority),
			"low":    len(q.lowPriority),
		},
	}
}

// Shutdown gracefully shuts down the queue
func (q *RequestQueue) Shutdown() {
	q.cancel()
	q.wg.Wait()
	
	// Drain remaining requests
	for {
		select {
		case req := <-q.highPriority:
			req.Response <- Response{
				StatusCode: 503,
				Error:      ErrShuttingDown,
			}
		case req := <-q.normalPriority:
			req.Response <- Response{
				StatusCode: 503,
				Error:      ErrShuttingDown,
			}
		case req := <-q.lowPriority:
			req.Response <- Response{
				StatusCode: 503,
				Error:      ErrShuttingDown,
			}
		default:
			return
		}
	}
}